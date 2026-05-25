#!/usr/bin/env bash
# scripts/oapi-breaking.sh — 用 oasdiff 检测 OpenAPI 破坏性变更。
#
# Usage:
#   make oapi-breaking                       # 默认对比 origin/master
#   OAPI_BREAKING_BASE_REF=v0.1.0 make oapi-breaking
#   OAPI_ALLOW_BREAKING=1 make oapi-breaking # 故意做 breaking（expand-contract）时跳过
#
# 行为：
#   1. 校验 oasdiff CLI 可用（不在版本就 go install pin 版本）。
#   2. 校验 base git ref 存在（不存在通常是 CI shallow clone，给出 fetch 指南）。
#   3. 跑 `oasdiff breaking --fail-on ERR <base>:api/openapi.yaml api/openapi.yaml`。
#      - ERR = 真破坏（删 endpoint、改返回 schema 字段类型等），非零退出
#      - WARN / INFO 不阻塞（兼容性可疑但通常允许，如新增 required field）
#
# 为什么不接进 make verify：
#   本地常没有 fresh origin/master，且 expand-contract 阶段会故意做 breaking。
#   verify 是"每次提交前都跑"的快速门禁，oapi-breaking 是"PR / 发版前"门禁，
#   定位不同。CI 在 PR job 单独跑这一步。
#
# 配置：
#   OAPI_BREAKING_BASE_REF  对比基线 git ref（默认 origin/master）
#   OAPI_BREAKING_SPEC      OpenAPI 文件路径（默认 api/openapi.yaml）
#   OAPI_ALLOW_BREAKING     非空时跳过检查直接返 0（应急 expand-contract 用，
#                           PR 描述里必须写明）
#   OASDIFF_VERSION         pin 版本（默认 v1.16.0；改动时同步更新 .github/workflows）

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BASE_REF="${OAPI_BREAKING_BASE_REF:-origin/master}"
SPEC="${OAPI_BREAKING_SPEC:-api/openapi.yaml}"
OASDIFF_VERSION="${OASDIFF_VERSION:-v1.16.0}"

if [ -n "${OAPI_ALLOW_BREAKING:-}" ]; then
  echo "oapi-breaking: OAPI_ALLOW_BREAKING set, skipping (PR 描述里写明 expand-contract 缘由)。"
  exit 0
fi

if [ ! -f "$SPEC" ]; then
  echo "oapi-breaking: $SPEC not found" >&2
  exit 1
fi

# ---- 1. 确保 oasdiff 可用 ----
ensure_oasdiff() {
  if command -v oasdiff >/dev/null 2>&1; then
    got=$(oasdiff --version 2>/dev/null | awk '{print $NF; exit}')
    # oasdiff --version 输出 "oasdiff version vX.Y.Z"。pin 不严：装新点没事。
    if [ -n "$got" ]; then
      echo "oasdiff $got: ok"
      return 0
    fi
  fi
  echo "Installing oasdiff $OASDIFF_VERSION ..."
  GOFLAGS= go install "github.com/oasdiff/oasdiff@$OASDIFF_VERSION"
  if ! command -v oasdiff >/dev/null 2>&1; then
    echo "oapi-breaking: oasdiff install succeeded but binary not on PATH." >&2
    echo "              Check that \$(go env GOBIN) or \$(go env GOPATH)/bin is in PATH." >&2
    exit 1
  fi
}
ensure_oasdiff

# ---- 2. 校验 base ref 存在 ----
if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null; then
  cat >&2 <<EOF
oapi-breaking: git ref "$BASE_REF" not found.

If you're in CI, ensure actions/checkout has fetch-depth: 0 so the base ref
is in the local clone. If you're local, fetch first:

  git fetch origin master

Then re-run. Or override the base ref:

  OAPI_BREAKING_BASE_REF=<ref> make oapi-breaking
EOF
  exit 1
fi

# 同 ref 比同 ref 没意义；告知 caller 是不是搞反了。
HEAD_SHA="$(git rev-parse HEAD)"
BASE_SHA="$(git rev-parse "$BASE_REF")"
if [ "$HEAD_SHA" = "$BASE_SHA" ]; then
  echo "oapi-breaking: HEAD == $BASE_REF ($BASE_SHA), nothing to compare."
  exit 0
fi

# ---- 3. 跑 oasdiff ----
# --fail-on ERR：只有 ERR 级（确凿 breaking）才非零退出，WARN/INFO 放过去。
# --format text：人类可读；CI 也直接吃 stdout 当 PR comment 原料。
# base 写 git ref 语法 "<ref>:<path>"；revision 写工作树文件路径。
echo "oapi-breaking: comparing $BASE_REF:$SPEC vs working tree $SPEC ..."
echo

if oasdiff breaking --fail-on ERR --format text "$BASE_REF:$SPEC" "$SPEC"; then
  echo
  echo "oapi-breaking: no ERR-level breaking changes."
else
  rc=$?
  echo >&2
  echo "oapi-breaking: ERR-level breaking changes detected (exit=$rc)." >&2
  echo "              If this is an expand-contract change you've coordinated," >&2
  echo "              re-run with OAPI_ALLOW_BREAKING=1 and document it in the PR." >&2
  exit "$rc"
fi
