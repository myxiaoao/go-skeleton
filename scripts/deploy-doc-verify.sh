#!/usr/bin/env bash
#
# 校验 docs/deploy.md 与 deploy/systemd/*.service 关键字段一致。
#
# 防止 systemd unit 修改后 deploy.md 忘了同步（路径 / 用户 / EnvironmentFile
# 等都是手动复制粘贴的）。校验项：
#   1. systemd unit 里出现的 ExecStart 路径 / WorkingDirectory / EnvironmentFile
#      在 deploy.md 中也能找到。
#   2. User= / Group= 值一致。
#   3. deploy.md 引用的 unit 文件名都真实存在。
#
# 退出码：0 = 一致；1 = 有不一致项，并把缺失项打出来。

set -euo pipefail

cd "$(dirname "$0")/.."

DEPLOY_DOC="docs/deploy.md"
UNIT_DIR="deploy/systemd"

if [ ! -f "$DEPLOY_DOC" ]; then
  echo "deploy-doc-verify: $DEPLOY_DOC missing"
  exit 1
fi

missing=()

check_in_doc() {
  local needle="$1" origin="$2"
  if ! grep -Fq -- "$needle" "$DEPLOY_DOC"; then
    missing+=("[$origin] '$needle' not found in $DEPLOY_DOC")
  fi
}

for unit in "$UNIT_DIR"/*.service; do
  [ -f "$unit" ] || continue
  base=$(basename "$unit")

  check_in_doc "$base" "$base"

  while IFS= read -r line; do
    key=${line%%=*}
    value=${line#*=}
    case "$key" in
      ExecStart|WorkingDirectory|EnvironmentFile)
        # ExecStart 可能带 args，只取第一个 token（路径）。
        path=${value%% *}
        check_in_doc "$path" "$base:$key"
        ;;
      User|Group)
        check_in_doc "$value" "$base:$key"
        ;;
    esac
  done < <(grep -E '^(ExecStart|WorkingDirectory|EnvironmentFile|User|Group)=' "$unit")
done

# 反向：deploy.md 引用的 *.service 文件名必须真的在 deploy/systemd/ 里。
while IFS= read -r ref; do
  if [ ! -f "$UNIT_DIR/$ref" ]; then
    missing+=("deploy.md references $ref but $UNIT_DIR/$ref does not exist")
  fi
done < <(grep -oE '[A-Za-z0-9_-]+\.service' "$DEPLOY_DOC" | sort -u)

if [ ${#missing[@]} -gt 0 ]; then
  echo "deploy-doc-verify: drift detected"
  printf '  - %s\n' "${missing[@]}"
  exit 1
fi

echo "deploy-doc-verify: docs/deploy.md and deploy/systemd/*.service agree."
