#!/usr/bin/env bash
# scripts/env-verify.sh
#
# 校验 config/ 里实际读取的 env key 与 .env.example 模板保持同步。新增
# 配置项忘了更新模板是高频疏忽——脚手架被复制后业务方读不到模板的字段
# 会以为是"可选项"，结果上线踩到默认值不符合预期。
#
# 校验规则：
#
# - 提取 config/*.go（不含 _test.go）里以下形式的 env key 字面量：
#     os.Getenv("KEY") / getEnvOrDefault("KEY", ...) / boolEnv("KEY", ...)
#     intEnv("KEY", ...) / int64Env / durationEnv / environmentEnv /
#     queueWeightsEnv
#   helper 第一参数都是 env key（项目约定，看 config/config.go）。
#
# - 提取 .env.example 里所有 `KEY=...` 行的 KEY（行首，忽略 # 注释）。
#
# - 双向比较：
#   * config 里读了 .env.example 没列 → fail，要求补模板
#   * .env.example 列了 config 不读 → fail，要么补代码、要么删模板的死字段
#
# 行号通过 grep -n 输出，便于编辑器跳转。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ ! -f .env.example ]; then
  echo "env-verify: .env.example 不存在" >&2
  exit 1
fi

# --- 1. 从 config/ 提取 env key ---
#
# 几个 helper（包括 os.Getenv）第一个参数都是 env key 字面量。
# 正则匹配 helper( "KEY" 或者 helper("KEY"。
# 排除 _test.go：测试里可能 mock 任意 env，不算项目约定。
#
# 用 -oP 取 PCRE，?<= 把 helper 名吃掉、只剩 "KEY" 部分，然后 tr 去掉引号。
# macOS 默认 grep 不支持 -P。改用 -E + sed 二段提取保险。

config_keys=$(
  grep -rhE --include='*.go' \
    '(os\.Getenv|getEnvOrDefault|boolEnv|intEnv|int64Env|durationEnv|environmentEnv|queueWeightsEnv)\("[A-Z][A-Z0-9_]*"' \
    config/ 2>/dev/null \
  | grep -v '_test\.go' \
  | grep -oE '"[A-Z][A-Z0-9_]*"' \
  | tr -d '"' \
  | sort -u
)

if [ -z "$config_keys" ]; then
  echo "env-verify: 没从 config/ 提取到任何 env key——确认正则是否还匹配现有 helper 签名" >&2
  exit 1
fi

# --- 2. 从 .env.example 提取 KEY ---
#
# 匹配 `^KEY=`（KEY 大写 + 下划线 + 数字），忽略 # 注释行和空行。
# = 后值不关心。

env_keys=$(
  grep -E '^[A-Z][A-Z0-9_]*=' .env.example \
  | sed -E 's/^([A-Z][A-Z0-9_]*)=.*/\1/' \
  | sort -u
)

if [ -z "$env_keys" ]; then
  echo "env-verify: .env.example 没有任何 KEY=... 行" >&2
  exit 1
fi

# --- 3. 双向比较 ---

missing_in_example=$(comm -23 <(printf '%s\n' "$config_keys") <(printf '%s\n' "$env_keys"))
missing_in_config=$(comm -13 <(printf '%s\n' "$config_keys") <(printf '%s\n' "$env_keys"))

# 允许的"模板里有但代码不读"的特例。比如纯标记或文档钉钉的字段。
# 当前没有；保留 hook 以后需要时往里加。
KNOWN_EXAMPLE_ONLY=""
if [ -n "$missing_in_config" ] && [ -n "$KNOWN_EXAMPLE_ONLY" ]; then
  missing_in_config=$(comm -23 <(printf '%s\n' "$missing_in_config") <(printf '%s\n' "$KNOWN_EXAMPLE_ONLY"))
fi

exit_code=0

if [ -n "$missing_in_example" ]; then
  echo "env-verify: config/ 读了下列 env key 但 .env.example 没列：" >&2
  while IFS= read -r k; do
    [ -z "$k" ] && continue
    loc=$(grep -nE "\"$k\"" config/*.go 2>/dev/null | grep -v '_test\.go:' | head -1 || true)
    echo "  - $k    ($loc)" >&2
  done <<<"$missing_in_example"
  echo >&2
  exit_code=1
fi

if [ -n "$missing_in_config" ]; then
  echo "env-verify: .env.example 列了下列 KEY 但 config/ 不读取（死字段或代码漏读）：" >&2
  while IFS= read -r k; do
    [ -z "$k" ] && continue
    loc=$(grep -nE "^$k=" .env.example | head -1 || true)
    echo "  - $k    (.env.example:$loc)" >&2
  done <<<"$missing_in_config"
  echo >&2
  exit_code=1
fi

if [ "$exit_code" -ne 0 ]; then
  echo "env-verify: 修复方向——补 .env.example 里的字段、或者删 config 里没用的读取、或者把模板里的死字段清掉。" >&2
  exit 1
fi

count=$(printf '%s\n' "$config_keys" | wc -l | tr -d ' ')
echo "env-verify: config/ ↔ .env.example 同步（$count keys）。"
