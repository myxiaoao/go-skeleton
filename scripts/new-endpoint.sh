#!/usr/bin/env bash
# scripts/new-endpoint.sh — scaffold the 5-file boilerplate for a new endpoint.
#
# Usage:
#   ./scripts/new-endpoint.sh <Name>
#
# Example:
#   ./scripts/new-endpoint.sh Order
#
# What it does:
#   把 internal/{handler,service,repository,model,task}/example.go 各复制一份，
#   仅在新建的 5 个文件里 sed 替换 Example→<Name> / example→<lower(Name)>。
#   不动 example_test.go（让你自己决定要不要 copy 测试），也不全仓 sed。
#
# What it does NOT do (intentional)：
#   - 不改 api/openapi.yaml（手动加 path + schema，再 make oapi）
#   - 不改 internal/server.go::newHTTPHandlers
#   - 不改 internal/router/router.go::Dependencies / 路由注册
#   末尾会打印这 3 步的提示，跑 make verify 之前手动补上。

set -euo pipefail

NAME="${1:-}"
if [ -z "$NAME" ]; then
  echo "usage: $0 <Name>"
  echo "  Name 必须 CamelCase 首字母大写，不含空格/特殊字符。"
  exit 1
fi

# 校验 CamelCase：开头大写字母，仅含字母数字。
if ! [[ "$NAME" =~ ^[A-Z][A-Za-z0-9]*$ ]]; then
  echo "error: NAME='$NAME' 必须 CamelCase 首字母大写，仅字母数字（如 Order、UserGroup）"
  exit 1
fi

# 小写形式：作为包内变量名 / 文件名 / 路径段。简化版（直接转小写），不做骆驼蛇形转换。
LOWER="$(echo "$NAME" | tr '[:upper:]' '[:lower:]')"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LAYERS=(handler service repository model task)
TARGETS=()

# 预检查：所有目标文件都不能已存在；example.go 模板都必须在。
for layer in "${LAYERS[@]}"; do
  src="internal/$layer/example.go"
  dst="internal/$layer/${LOWER}.go"
  if [ ! -f "$src" ]; then
    echo "error: 模板缺失：$src"
    exit 1
  fi
  if [ -f "$dst" ]; then
    echo "error: 已存在：$dst（拒绝覆盖；先 rm 或换 NAME）"
    exit 1
  fi
  TARGETS+=("$src:$dst")
done

# 复制 + 仅在新文件里替换 Example/example
for pair in "${TARGETS[@]}"; do
  src="${pair%%:*}"
  dst="${pair##*:}"
  cp "$src" "$dst"
  # sed -i 在 macOS / GNU 上行为有差异；用 -i.bak 然后删 backup，兼容两边。
  sed -i.bak -E "s/Example/${NAME}/g; s/example/${LOWER}/g" "$dst"
  rm -f "${dst}.bak"
  echo "✓ created $dst"
done

cat <<EOF

下一步手动补：

  1. api/openapi.yaml
     - 在 paths: 下加 /api/v1/${LOWER}s ：列表 / 创建 / ...
     - 在 components.schemas 下加 ${NAME} 业务 schema
     - 跑 make oapi 生成 oapi.gen.go

  2. internal/server.go::newHTTPHandlers
     - new${NAME}Repository / new${NAME}Service / new${NAME}Handler
     - 挂到 HTTPHandlers + APIServer

  3. internal/router/router.go::Dependencies
     - 加 ${NAME} *handler.${NAME}Handler 字段
     - 在合适的 register*Routes 里注册路由

补完后跑：
  make verify
EOF
