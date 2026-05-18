#!/usr/bin/env bash
# scripts/docs-verify.sh
#
# Make sure AGENTS.md and CLAUDE.md don't drift on the sections that should
# stay identical between them. The two files are maintained in parallel for
# different AI assistants; missing updates here is the most common form of
# silent rot.
#
# Compared sections (## headings, both files must contain identical content):
#   分层规则：handler → service → repository
#   依赖装配（手写 DI）
#   统一响应协议
#   i18n
#   JWT 鉴权
#   异步队列
#   context 传递（硬约束）
#   环境变量
#   审计日志
#   pkg/ 边界
#   AI 助手提示（最高频违反，每次进项目先扫这段）
#   写代码时常犯的错（已知会触发返工）
#   API 契约：OpenAPI 3.1
#   验证命令
#   测试约定
#   Git Workflow
#
# Sections NOT compared (intentional differences):
#   - Title + leading quote block (one mentions Claude, the other Codex)
#   - 技术栈 / 顶层目录 (allowed to differ on minor wording)
#   - 通用工作准则 (AGENTS.md only — inlines global rules for Codex)
#   - 目录树速查 (tree comment "本文件 / 与本文件并存维护" differs by design)

set -euo pipefail

CLAUDE="${1:-CLAUDE.md}"
AGENTS="${2:-AGENTS.md}"

for f in "$CLAUDE" "$AGENTS"; do
  if [ ! -f "$f" ]; then
    echo "docs-verify: $f not found" >&2
    exit 1
  fi
done

SECTIONS=(
  "分层规则：handler → service → repository"
  "依赖装配（手写 DI）"
  "统一响应协议"
  "i18n"
  "JWT 鉴权"
  "异步队列"
  "context 传递（硬约束）"
  "环境变量"
  "审计日志"
  "pkg/ 边界"
  "AI 助手提示（最高频违反，每次进项目先扫这段）"
  "写代码时常犯的错（已知会触发返工）"
  "API 契约：OpenAPI 3.1"
  "验证命令"
  "测试约定"
  "Git Workflow"
)

# extract <file> <section-title> → stdout = section body up to the next H2
extract() {
  local file="$1" title="$2"
  awk -v t="## $title" '
    $0 == t          { capture = 1; next }
    capture && /^## / { exit }
    capture           { print }
  ' "$file"
}

mismatched=0
for section in "${SECTIONS[@]}"; do
  claude_body="$(extract "$CLAUDE" "$section")"
  agents_body="$(extract "$AGENTS" "$section")"

  if [ -z "$claude_body" ]; then
    echo "docs-verify: section [$section] missing in $CLAUDE" >&2
    mismatched=1
    continue
  fi
  if [ -z "$agents_body" ]; then
    echo "docs-verify: section [$section] missing in $AGENTS" >&2
    mismatched=1
    continue
  fi

  if [ "$claude_body" != "$agents_body" ]; then
    echo "docs-verify: section [$section] differs between $CLAUDE and $AGENTS" >&2
    diff <(echo "$claude_body") <(echo "$agents_body") | head -40 >&2 || true
    echo "" >&2
    mismatched=1
  fi
done

if [ $mismatched -ne 0 ]; then
  echo "" >&2
  echo "docs-verify: AGENTS.md and CLAUDE.md are out of sync." >&2
  echo "             Apply the same change to both files and re-run." >&2
  exit 1
fi

echo "docs-verify: AGENTS.md and CLAUDE.md shared sections in sync."
