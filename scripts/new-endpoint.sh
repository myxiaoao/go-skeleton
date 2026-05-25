#!/usr/bin/env bash
# scripts/new-endpoint.sh — 给新模块脚手架 5 个分层文件 + 测试镜像 +
# server.go / router.go 装配注入。
#
# Usage:
#   ./scripts/new-endpoint.sh <Name>
#
# Example:
#   ./scripts/new-endpoint.sh Order
#
# What it does:
#   1. internal/{handler,service,repository,model,task}/example.go 各复制
#      一份，仅在新文件里把 Example→<Name> / example→<lower(Name)>。
#   2. 给 handler / service / repository 三层生成最小测试骨架——不是直
#      接复制 example_test.go（同包 helper 重名 + 跨包类型引用都会让纯
#      sed 复制编译不过），而是写一个空 stub：占位 init() + 一个 skip
#      的 TestPlaceholder，让 go test ./... 不报"no test files"，新开发
#      者补真实用例时直接添加，参考 example_test.go 即可。
#   3. 改 internal/server.go：
#      - HTTPHandlers struct 加 <Name> *handler.<Name>Handler 字段
#      - newHTTPHandlers 加 repo / service / handler 装配链
#      - HTTPHandlers return literal 加 <Name>: <name>H,
#      （锚点：<new-endpoint:handlers-fields/deps/construct/return>）
#   4. 改 internal/router/router.go：
#      - Dependencies 加 <Name> *handler.<Name>Handler 字段
#      - RegisterRoutes 调用新增的 register<Name>Routes
#      - 文件尾追加 register<Name>Routes 函数（默认列表 / 创建 / EnqueueTask）
#
# What it does NOT do (有意保持人工)：
#   - 不改 api/openapi.yaml：业务字段千变万化，自动注入风险大。脚本结
#     尾打印一段 yaml stub 给你贴。
#   - 不改 internal/oapi/oapi.gen.go：跑 make oapi 在 yaml 改完后生成。
#   - 不改 internal/handler/openapi.go::APIServer：它实现 oapi.ServerInterface，
#     依赖 make oapi 生成的接口方法。脚本结尾打印 APIServer 字段 + 方
#     法骨架给你贴。
#   - 不自动跑 go build：在你补完 yaml + APIServer 之前编译必然不过，
#     脚本输出预期的"下一步"清单代替自动验证。
#
# 跨平台说明：BSD sed (macOS) 与 GNU sed 在 `i\` / `-i` 上行为不一致，
# 本脚本所有"按锚点注入"用 awk 实现，避免方言差异；仅"全文替换 token"
# 还用 sed（-i.bak 然后删 .bak 是跨平台习惯写法）。

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
# 哪些层生成测试 stub。model / task 是数据结构层，没什么测试，跳过。
TEST_LAYERS=(handler service repository)

# -------- 预检查 --------

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
done

for layer in "${TEST_LAYERS[@]}"; do
  dst="internal/$layer/${LOWER}_test.go"
  if [ -f "$dst" ]; then
    echo "error: 测试已存在：$dst（拒绝覆盖）"
    exit 1
  fi
done

# server.go / router.go 锚点都必须在；不在则文件被 hand-edit 破坏过，
# 停手。锚点 = 以 "// NEH <name>" 开头的注释行；用全行匹配避免 godoc
# 里提到锚点名时被误命中（这是上一次脚本插错位置的根因）。
require_marker() {
  local file="$1"
  local marker="$2"
  if ! grep -qE "^[[:space:]]*// NEH ${marker}\$" "$file"; then
    echo "error: $file 缺锚点 '// NEH ${marker}'，无法幂等注入。"
    exit 1
  fi
}
require_marker internal/server.go 'handlers-fields'
require_marker internal/server.go 'handlers-deps'
require_marker internal/server.go 'handlers-construct'
require_marker internal/server.go 'handlers-return'
require_marker internal/router/router.go 'deps-fields'
require_marker internal/router/router.go 'routes-register'

# 跨平台 sed -i：BSD sed 要求 -i 后跟扩展名（即使是空串），用 -i.bak +
# 删 .bak 兼容 macOS 与 GNU。仅给"全文替换"用，不给"插入"用。
sed_subst() {
  local file="$1"
  shift
  sed -i.bak "$@" "$file"
  rm -f "${file}.bak"
}

# insert_before_marker <file> <marker> <line>
#
# 在 <file> 里把 <line> 插到首个匹配 "// NEH <marker>" 全行的注释**之前**；
# 用 awk 跨平台实现（BSD sed 的 `i\` 多行处理与 GNU 不一致，容易把缩进
# 混进锚点行）。<line> 原样保留缩进（包括前导 tab）；多行注入请多次调用。
#
# 全行匹配（^[[:space:]]*// NEH <marker>$）避免 godoc 里提到锚点名时被
# 误命中——上一次脚本就是没限定全行匹配，把注入行塞进 godoc 注释中间。
insert_before_marker() {
  local file="$1"
  local marker="$2"
  local line="$3"
  local tmp
  tmp="$(mktemp)"
  awk -v marker="$marker" -v line="$line" '
    {
      if ($0 ~ ("^[[:space:]]*// NEH " marker "$")) {
        print line
      }
      print
    }
  ' "$file" >"$tmp"
  mv "$tmp" "$file"
}

# -------- 1. 复制分层文件 + sed --------

for layer in "${LAYERS[@]}"; do
  src="internal/$layer/example.go"
  dst="internal/$layer/${LOWER}.go"
  cp "$src" "$dst"
  sed_subst "$dst" -E "s/Example/${NAME}/g; s/example/${LOWER}/g"
  echo "✓ created $dst"
done

# -------- 2. 生成测试 stub --------
#
# 直接复制 example_test.go 走不通：同包级 helper（setupRouter / dbCapture
# 等）会因重名编译失败；跨包类型引用（service.ExampleRepository）也不在
# sed 替换范围里。改成生成最小 stub，让 go test 不报 "no test files"，
# 新开发者按 example_test.go 的风格手补真实用例。

for layer in "${TEST_LAYERS[@]}"; do
  dst="internal/$layer/${LOWER}_test.go"
  cat > "$dst" <<EOF
package ${layer}

import (
	"testing"

	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

func init() {
	// 静音业务日志，避免测试输出被审计 / trace log 刷屏；与 example_test.go
	// 风格保持一致。handler / service / repository 测试都需要这个。
	applog.SetLogger(zap.NewNop())
}

// TestPlaceholder 是 new-endpoint.sh 生成的占位测试，让 go test 不报
// "no test files" 即可。删掉本函数 + 按 example_${layer}.go 的同名
// 测试风格（标准库 testing + 手写 mock）补真实用例。
func TestPlaceholder(t *testing.T) {
	t.Skip("TODO: replace with real ${NAME} ${layer} tests")
}
EOF
  echo "✓ created $dst"
done

# -------- 3. 注入 server.go 装配 --------
#
# 缩进用 tab，与 server.go 风格一致；插入完跑一次 gofmt 兜底。
# 一次只插一行；多行（如 handlers-deps 的两行 repo/service 声明）连续
# 调用两次——awk 第二次会匹配同一个 marker（marker 行没被消耗），把第
# 二行 print 在 marker 之前，最终顺序：line1, line2, marker。

insert_before_marker internal/server.go 'handlers-fields' \
  "	${NAME} *handler.${NAME}Handler"

insert_before_marker internal/server.go 'handlers-deps' \
  "	${LOWER}Repository := repository.New${NAME}Repository(db)"
insert_before_marker internal/server.go 'handlers-deps' \
  "	${LOWER}Service := service.New${NAME}Service(${LOWER}Repository, reg.Queue)"

insert_before_marker internal/server.go 'handlers-construct' \
  "	${LOWER}H := handler.New${NAME}Handler(${LOWER}Service)"

insert_before_marker internal/server.go 'handlers-return' \
  "		${NAME}: ${LOWER}H,"

echo "✓ patched internal/server.go"

# -------- 4. 注入 router.go --------

insert_before_marker internal/router/router.go 'deps-fields' \
  "	${NAME} *handler.${NAME}Handler"

insert_before_marker internal/router/router.go 'routes-register' \
  "	register${NAME}Routes(r, deps)"

cat >> internal/router/router.go <<EOF

// register${NAME}Routes 挂 /${LOWER}s/* 路由：默认按 example 模板生成
// 列表 / 创建 / EnqueueTask 三条。生成后按真实业务调整：删多余路由、
// 加 AuthRequired 中间件、调路径形态。
func register${NAME}Routes(r *gin.RouterGroup, deps Dependencies) {
	if deps.${NAME} == nil {
		return
	}

	${LOWER}s := r.Group("/${LOWER}s")
	${LOWER}s.GET("", deps.${NAME}.List)
	${LOWER}s.POST("", deps.${NAME}.Create)
	${LOWER}s.POST("/tasks", deps.${NAME}.EnqueueTask)
}
EOF

echo "✓ patched internal/router/router.go"

# gofmt 兜底——awk 插入的缩进虽然按风格写好，但 gofmt 能把意外空白
# / tab/space 混用整理掉。
gofmt -w internal/server.go internal/router/router.go

# 注入后断言：每个锚点都还在文件里（注入是"在前插入"，锚点行本身保留）；
# 不在则脚本逻辑出 bug，应该停下来让用户复盘。
require_marker internal/server.go 'handlers-fields'
require_marker internal/server.go 'handlers-deps'
require_marker internal/server.go 'handlers-construct'
require_marker internal/server.go 'handlers-return'
require_marker internal/router/router.go 'deps-fields'
require_marker internal/router/router.go 'routes-register'

# -------- 5. 打印剩余手工步骤 --------

cat <<EOF

✅ 5 个分层文件 + 测试镜像已生成，server.go / router.go 装配已注入。
   现在仓库**编译不过**——APIServer 还没实现新 endpoint 对应的
   oapi.ServerInterface 方法。下面 3 步补完后 make verify 才会绿：

────────────────────────────────────────────────────────────
1. api/openapi.yaml：加路径 + schema
────────────────────────────────────────────────────────────
   把下面这段贴进 paths（按真实业务字段改）：

   /api/v1/${LOWER}s:
     get:
       operationId: list${NAME}s
       summary: 列表
       parameters:
         - name: limit
           in: query
           schema: { type: integer, minimum: 1, maximum: 100 }
         - name: offset
           in: query
           schema: { type: integer, minimum: 0 }
       responses:
         '200':
           description: OK
     post:
       operationId: create${NAME}
       summary: 创建
       requestBody:
         required: true
         content:
           application/json:
             schema: { \$ref: '#/components/schemas/Create${NAME}Req' }
       responses:
         '200':
           description: OK

   /api/v1/${LOWER}s/tasks:
     post:
       operationId: enqueue${NAME}Task
       summary: 投递异步任务
       responses:
         '200':
           description: OK

   components.schemas 下加：
   Create${NAME}Req:
     type: object
     required: [name]
     properties:
       name:
         type: string
         maxLength: 255

   然后跑：
     make oapi

────────────────────────────────────────────────────────────
2. internal/handler/openapi.go::APIServer：加字段 + 方法
────────────────────────────────────────────────────────────
   字段：
     ${NAME} *${NAME}Handler

   方法骨架（按 oapi 生成的接口签名补 params 类型）：
     func (s *APIServer) List${NAME}s(c *gin.Context, _ oapi.List${NAME}sParams) {
         s.${NAME}.List(c)
     }
     func (s *APIServer) Create${NAME}(c *gin.Context) { s.${NAME}.Create(c) }
     func (s *APIServer) Enqueue${NAME}Task(c *gin.Context) { s.${NAME}.EnqueueTask(c) }

   internal/server.go 里 APIServer struct 实例化也要补一个：
     ${NAME}: ${LOWER}H,

────────────────────────────────────────────────────────────
3. make verify
────────────────────────────────────────────────────────────
   通过即合 PR。验证链会跑 architecture-verify / env-verify 等所有门禁。
EOF
