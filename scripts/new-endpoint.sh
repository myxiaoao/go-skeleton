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

# -------- 2. 生成测试模板 --------
#
# 模板按 example_<layer>_test.go 的风格生成可直接编译跑通的最小用例；
# 不复制 example_test.go 原文（同包 helper 重名 / 跨包类型引用走不通），
# 而是用 ${NAME}/${LOWER} 前缀避开冲突：
#   handler:    setup${NAME}Router / mock${NAME}Repo / mock${NAME}Queue
#   service:    mock${NAME}Repo / mock${NAME}Queue
#   repository: ${LOWER}DryRunDB / ${LOWER}Capture
#
# 假设：新模块沿用 example 模板的方法集（Create / List / EnqueueTask /
# Process${NAME}），即 NewExampleService→New${NAME}Service / NewExampleHandler→
# New${NAME}Handler 接口形态。如果改了业务签名（删 EnqueueTask、加新方法等），
# 删掉对应 *_test.go 用例再按真实接口手补。这比生成 TestPlaceholder Skip
# 强：至少能跑通一遍证明业务接口与脚手架的形态一致。

# handler 层测试模板
cat > "internal/handler/${LOWER}_test.go" <<EOF
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/model"
	"go-skeleton/internal/service"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/response"
	"go-skeleton/pkg/validator"
)

// mock${NAME}Repo 提供 service.${NAME}Repository 接口的可注入 mock，
// 用 func 字段而不是 if/else 让每个用例只写要关心的行为。
type mock${NAME}Repo struct {
	createFunc func(ctx context.Context, ${LOWER} *model.${NAME}) error
	listFunc   func(ctx context.Context, limit, offset int) ([]model.${NAME}, int64, error)
}

func (m *mock${NAME}Repo) Create(ctx context.Context, e *model.${NAME}) error {
	return m.createFunc(ctx, e)
}

func (m *mock${NAME}Repo) List(ctx context.Context, limit, offset int) ([]model.${NAME}, int64, error) {
	return m.listFunc(ctx, limit, offset)
}

type mock${NAME}Queue struct {
	available   bool
	enqueueFunc func(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

func (m *mock${NAME}Queue) Available() bool { return m.available }
func (m *mock${NAME}Queue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return m.enqueueFunc(ctx, t, opts...)
}

// setup${NAME}Router 构造一个仅注册 /${LOWER}s/* 路由的最小 gin engine，
// 替代去 internal/server.go 拉整套依赖。注入"试探性 trace_id"中间件让
// response.metadata.trace_id 测得到。
func setup${NAME}Router(repo service.${NAME}Repository, queues ...service.${NAME}Queue) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("trace_id", "test-trace")
		c.Next()
	})

	var queue service.${NAME}Queue
	if len(queues) > 0 {
		queue = queues[0]
	}
	svc := service.New${NAME}Service(repo, queue)
	h := New${NAME}Handler(svc)
	r.POST("/${LOWER}s", h.Create)
	r.GET("/${LOWER}s", h.List)
	r.POST("/${LOWER}s/tasks", h.EnqueueTask)
	return r
}

func init() {
	// 静音业务日志，避免审计 / trace log 刷屏 stdout；validator 初始化让
	// binding 校验报错文案非空（与 example_test.go 一致）。
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
}

// Test${NAME}HandlerCreateSuccess 是 new-endpoint.sh 生成的"smoke 模板"，
// 验证 handler → service → repo 的接通能正常返 code=0。按真实业务字段
// 调整 payload 后追加 validation / database error / 边界值用例，参考
// example_test.go 的写法。
func Test${NAME}HandlerCreateSuccess(t *testing.T) {
	repo := &mock${NAME}Repo{
		createFunc: func(_ context.Context, e *model.${NAME}) error {
			e.ID = 1
			return nil
		},
	}
	router := setup${NAME}Router(repo)

	req := httptest.NewRequest(http.MethodPost, "/${LOWER}s",
		strings.NewReader(\`{"name":"smoke"}\`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
}
EOF
echo "✓ created internal/handler/${LOWER}_test.go"

# service 层测试模板
cat > "internal/service/${LOWER}_test.go" <<EOF
package service

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/model"
	applog "go-skeleton/pkg/log"
)

type mock${NAME}Repo struct {
	createFunc func(ctx context.Context, ${LOWER} *model.${NAME}) error
	listFunc   func(ctx context.Context, limit, offset int) ([]model.${NAME}, int64, error)
}

func (m *mock${NAME}Repo) Create(ctx context.Context, e *model.${NAME}) error {
	return m.createFunc(ctx, e)
}

func (m *mock${NAME}Repo) List(ctx context.Context, limit, offset int) ([]model.${NAME}, int64, error) {
	return m.listFunc(ctx, limit, offset)
}

type mock${NAME}Queue struct {
	available   bool
	enqueueFunc func(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

func (m *mock${NAME}Queue) Available() bool { return m.available }
func (m *mock${NAME}Queue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return m.enqueueFunc(ctx, t, opts...)
}

func init() {
	applog.SetLogger(zap.NewNop())
}

// Test${NAME}ServiceCreateSuccess 是 new-endpoint.sh 生成的 smoke 模板。
// 按真实业务追加边界（重复创建 / 关联失败 / 队列不可用）、并参考
// example_test.go 的 errcode.Error 断言写法补真实错误码用例。
func Test${NAME}ServiceCreateSuccess(t *testing.T) {
	repo := &mock${NAME}Repo{
		createFunc: func(_ context.Context, e *model.${NAME}) error {
			e.ID = 1
			return nil
		},
	}
	svc := New${NAME}Service(repo, nil)

	got, err := svc.Create(context.Background(), &Create${NAME}Req{Name: "smoke"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("ID = %d, want 1", got.ID)
	}
	if got.Name != "smoke" {
		t.Fatalf("Name = %q, want smoke", got.Name)
	}
}
EOF
echo "✓ created internal/service/${LOWER}_test.go"

# repository 层测试模板
cat > "internal/repository/${LOWER}_test.go" <<EOF
package repository

import (
	"context"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"go-skeleton/internal/model"
)

// ${LOWER}Capture 收集 GORM 回调里看到的 SQL，给断言用。DryRun 模式下
// 真正不会发到 DB，但 callback 链照走，足以验证 repo 生成的 SQL 形状。
type ${LOWER}Capture struct {
	createCalls int
	queries     []string
}

// new${NAME}DryRunDB 起一个 DryRun 模式的 gorm.DB，不连真实 Postgres。
// 提供独立 helper 避免和 example_test.go 的 newDryRunDB 同名冲突。
func new${NAME}DryRunDB(t *testing.T, capture *${LOWER}Capture) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(postgres.Open("postgres://u:p@127.0.0.1:5432/db?sslmode=disable"), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open dry run: %v", err)
	}
	if capture == nil {
		return db
	}

	if err := db.Callback().Create().After("gorm:create").Register("test:${LOWER}_capture_create", func(tx *gorm.DB) {
		capture.createCalls++
		if tx.Statement != nil {
			capture.queries = append(capture.queries, tx.Statement.SQL.String())
		}
	}); err != nil {
		t.Fatalf("register create callback: %v", err)
	}
	return db
}

// Test${NAME}RepositoryCreate 是 new-endpoint.sh 生成的 smoke 模板，
// 验证 Create 至少触发了一次 INSERT。参考 example_test.go 追加
// query 形状断言（ORDER BY / LIMIT / 字段名等）。
func Test${NAME}RepositoryCreate(t *testing.T) {
	capture := &${LOWER}Capture{}
	db := new${NAME}DryRunDB(t, capture)
	repo := New${NAME}Repository(db)

	if err := repo.Create(context.Background(), &model.${NAME}{Name: "smoke"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if capture.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", capture.createCalls)
	}
}
EOF
echo "✓ created internal/repository/${LOWER}_test.go"

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
