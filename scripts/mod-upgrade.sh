#!/usr/bin/env bash
# scripts/mod-upgrade.sh — 自动升级 go.mod 直接依赖到最新 patch/minor。
#
# Usage:
#   ./scripts/mod-upgrade.sh            # 干跑：列出会升的依赖
#   APPLY=1 ./scripts/mod-upgrade.sh    # 真升：对每个模块 go get → tidy → verify
#
# 行为：
#   - 只看 go list -m -u 输出里 Indirect != true 的项（直接依赖）。
#   - 通过 semver MAJOR 比对判断：
#       MAJOR 升 → 不动，打印提示让人工评估（API 通常会破）。
#       MINOR / PATCH 升 → 加入升级列表。
#   - APPLY=1 时逐个升：go get <mod>@<ver> → go mod tidy → make verify。
#     verify 挂 → git checkout -- go.mod go.sum 回滚 + 打印挂的那一项 + 退出。
#
# 不做：
#   - 不升 v0.x.x 跨 minor（v0 视同 unstable，按 major 处理）。
#   - 不递归处理 indirect 依赖（由 go mod tidy 自然带）。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! command -v go >/dev/null; then
  echo "error: go not found in PATH"
  exit 1
fi
if ! command -v jq >/dev/null; then
  echo "error: jq not found in PATH; install via 'brew install jq' / apt install jq"
  exit 1
fi

# 提取直接依赖中有 Update 字段的项；输出 path<TAB>cur<TAB>new
# 注：Indirect 字段对直接依赖会缺失（null），(.Indirect // false) == false 比 != true 更明确。
UPDATES="$(go list -m -u -json all 2>/dev/null \
  | jq -r 'select(.Update != null and (.Indirect // false) == false) | "\(.Path)\t\(.Version)\t\(.Update.Version)"')"

if [ -z "$UPDATES" ]; then
  echo "no direct deps to upgrade."
  exit 0
fi

semver_major() {
  # 从 v1.2.3 / v1.2.3-rc.1 / v0.x.y / v1 提取 major 段；v0 单独视为 unstable major。
  local v="${1#v}"
  local major="${v%%.*}"
  echo "$major"
}

PATCH_MINOR=()  # 待升列表
MAJOR_SKIP=()   # 仅打印不升

while IFS=$'\t' read -r path cur new; do
  cur_major="$(semver_major "$cur")"
  new_major="$(semver_major "$new")"
  if [ "$cur_major" != "$new_major" ] || [ "$cur_major" = "0" ]; then
    # 跨 major，或 v0.x 任意改动 → skip
    MAJOR_SKIP+=("$path $cur → $new")
  else
    PATCH_MINOR+=("$path@$new")
  fi
done <<< "$UPDATES"

echo "==> direct deps with patch/minor updates (will upgrade)"
if [ "${#PATCH_MINOR[@]}" -eq 0 ]; then
  echo "  (none)"
else
  for it in "${PATCH_MINOR[@]}"; do echo "  + $it"; done
fi

echo
echo "==> major / v0.x updates (skipped — review manually)"
if [ "${#MAJOR_SKIP[@]}" -eq 0 ]; then
  echo "  (none)"
else
  for it in "${MAJOR_SKIP[@]}"; do echo "  ! $it"; done
fi

if [ "${APPLY:-0}" != "1" ]; then
  echo
  echo "dry run only. re-run with APPLY=1 to actually upgrade."
  exit 0
fi

if [ "${#PATCH_MINOR[@]}" -eq 0 ]; then
  echo "nothing to apply."
  exit 0
fi

echo
echo "==> applying upgrades (one at a time, with rollback on verify failure)"

# 用 stash 锁定升级前的 go.mod / go.sum 基线。每个模块升级成功后保留累积
# 改动；失败时通过 stash 还原到初始基线，而不是 git checkout —— 后者只
# 回滚最后一步，前面 N-1 次成功的升级也会被一起抹掉。
#
# stash 的 ref 通过 commit hash 锁定（不依赖 stash@{0} 索引），防止脚本运
# 行期间用户在另一个终端 stash 引入位移。
baseline_stash=""
if ! git diff --quiet -- go.mod go.sum 2>/dev/null; then
  echo "error: go.mod / go.sum already dirty; commit or stash before running APPLY=1"
  exit 1
fi
baseline_stash="$(git stash create -- go.mod go.sum 2>/dev/null || true)"

restore_baseline() {
  if [ -n "$baseline_stash" ]; then
    git checkout "$baseline_stash" -- go.mod go.sum
  else
    # 没基线（脚本刚开始时 working tree 没改动），直接 checkout HEAD 还原。
    git checkout -- go.mod go.sum
  fi
}

failed_at=""
for it in "${PATCH_MINOR[@]}"; do
  echo "----- $it -----"
  if ! go get "$it"; then
    failed_at="$it (go get)"
    break
  fi
  if ! go mod tidy; then
    failed_at="$it (go mod tidy)"
    break
  fi
  if ! make verify; then
    failed_at="$it (make verify)"
    break
  fi
done

if [ -n "$failed_at" ]; then
  echo
  echo "✗ upgrade failed at: $failed_at"
  echo "  rolling back ALL upgrades to baseline (this run produced no changes)..."
  restore_baseline
  exit 1
fi

echo
echo "✓ all patch/minor upgrades applied + verify green."
echo "  review the diff (git diff go.mod go.sum) then commit."
