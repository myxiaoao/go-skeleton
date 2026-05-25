//go:build ignore

// new-endpoint 给新模块脚手架 5 个分层文件 + 测试模板 +
// server.go / router.go 装配注入。
//
// 入口：
//
//	go run scripts/new-endpoint.go <Name>
//	make new-endpoint NAME=<Name>       # 推荐
//
// 示例：
//
//	go run scripts/new-endpoint.go Order
//
// 做什么：
//  1. internal/{handler,service,repository,model,task}/example.go 各复制
//     一份，仅在新文件里把 Example→<Name> / example→<lower(Name)>。
//  2. 给 handler / service / repository 三层生成可直接编译跑通的最小测试
//     模板（沿用 example_test.go 的"标准库 testing + 手写 mock"风格，类名
//     带 ${NAME}/${LOWER} 前缀避开同包重名）。
//  3. 改 internal/server.go：按 // NEH 锚点注入字段 + 装配链 + return 字段。
//  4. 改 internal/router/router.go：注入 Dependencies 字段 + 注册调用，并
//     append register${NAME}Routes 函数。
//
// 不做：
//   - 不改 api/openapi.yaml：业务字段千变万化，自动注入风险大。脚本结尾
//     打印 yaml stub 给用户贴。
//   - 不改 internal/oapi/oapi.gen.go：跑 make oapi 在 yaml 改完后生成。
//   - 不改 internal/handler/openapi.go::APIServer：依赖 make oapi 生成的
//     接口签名。脚本结尾打印 APIServer 字段 + 方法骨架给用户贴。
//   - 不自动跑 go build：在你补完 yaml + APIServer 之前编译必然不过。
//
// 与旧 bash 版的语义差异：
//   - 整个流程纯 Go：不依赖 sed / awk 方言（BSD vs GNU），跨平台一致。
//   - 锚点注入用全行匹配（与旧 awk 版一致），避免 godoc 里提到锚点名时被
//     误命中。
//   - 注入后 ast.ParseFile 校验语法，破即报；最后 gofmt -w 整理缩进。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	layers     = []string{"handler", "service", "repository", "model", "task"}
	testLayers = []string{"handler", "service", "repository"} // model/task 是数据结构层，不生成测试

	camelCaseRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "" {
		fatal(fmt.Errorf(`usage: new-endpoint <Name>
  Name 必须 CamelCase 首字母大写，不含空格/特殊字符（如 Order、UserGroup）`))
	}
	name := os.Args[1]
	if !camelCaseRe.MatchString(name) {
		fatal(fmt.Errorf("NAME=%q 必须 CamelCase 首字母大写，仅字母数字（如 Order、UserGroup）", name))
	}
	lower := strings.ToLower(name)

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	// ---- 预检查 ----
	for _, layer := range layers {
		src := fmt.Sprintf("internal/%s/example.go", layer)
		dst := fmt.Sprintf("internal/%s/%s.go", layer, lower)
		if _, err := os.Stat(src); err != nil {
			fatal(fmt.Errorf("模板缺失：%s", src))
		}
		if _, err := os.Stat(dst); err == nil {
			fatal(fmt.Errorf("已存在：%s（拒绝覆盖；先 rm 或换 NAME）", dst))
		}
	}
	for _, layer := range testLayers {
		dst := fmt.Sprintf("internal/%s/%s_test.go", layer, lower)
		if _, err := os.Stat(dst); err == nil {
			fatal(fmt.Errorf("测试已存在：%s（拒绝覆盖）", dst))
		}
	}
	markers := []struct {
		file, name string
	}{
		{"internal/server.go", "handlers-fields"},
		{"internal/server.go", "handlers-deps"},
		{"internal/server.go", "handlers-construct"},
		{"internal/server.go", "handlers-return"},
		{"internal/router/router.go", "deps-fields"},
		{"internal/router/router.go", "routes-register"},
	}
	for _, m := range markers {
		if err := requireMarker(m.file, m.name); err != nil {
			fatal(err)
		}
	}

	// ---- 1. 复制 5 个分层文件 ----
	for _, layer := range layers {
		src := fmt.Sprintf("internal/%s/example.go", layer)
		dst := fmt.Sprintf("internal/%s/%s.go", layer, lower)
		if err := copyAndRename(src, dst, name, lower); err != nil {
			fatal(err)
		}
		fmt.Printf("✓ created %s\n", dst)
	}

	// ---- 2. 生成 3 个测试模板 ----
	if err := writeFile(
		fmt.Sprintf("internal/handler/%s_test.go", lower),
		renderHandlerTest(name, lower),
	); err != nil {
		fatal(err)
	}
	fmt.Printf("✓ created internal/handler/%s_test.go\n", lower)

	if err := writeFile(
		fmt.Sprintf("internal/service/%s_test.go", lower),
		renderServiceTest(name, lower),
	); err != nil {
		fatal(err)
	}
	fmt.Printf("✓ created internal/service/%s_test.go\n", lower)

	if err := writeFile(
		fmt.Sprintf("internal/repository/%s_test.go", lower),
		renderRepositoryTest(name, lower),
	); err != nil {
		fatal(err)
	}
	fmt.Printf("✓ created internal/repository/%s_test.go\n", lower)

	// ---- 3. 注入 server.go ----
	serverPatches := []struct {
		marker string
		lines  []string
	}{
		{"handlers-fields", []string{
			fmt.Sprintf("\t%s *handler.%sHandler", name, name),
		}},
		{"handlers-deps", []string{
			fmt.Sprintf("\t%sRepository := repository.New%sRepository(db)", lower, name),
			fmt.Sprintf("\t%sService := service.New%sService(%sRepository, reg.Queue)", lower, name, lower),
		}},
		{"handlers-construct", []string{
			fmt.Sprintf("\t%sH := handler.New%sHandler(%sService)", lower, name, lower),
		}},
		{"handlers-return", []string{
			fmt.Sprintf("\t\t%s: %sH,", name, lower),
		}},
	}
	for _, p := range serverPatches {
		for _, line := range p.lines {
			if err := insertBeforeMarker("internal/server.go", p.marker, line); err != nil {
				fatal(err)
			}
		}
	}
	fmt.Println("✓ patched internal/server.go")

	// ---- 4. 注入 router.go ----
	routerPatches := []struct {
		marker string
		line   string
	}{
		{"deps-fields", fmt.Sprintf("\t%s *handler.%sHandler", name, name)},
		{"routes-register", fmt.Sprintf("\tregister%sRoutes(r, deps)", name)},
	}
	for _, p := range routerPatches {
		if err := insertBeforeMarker("internal/router/router.go", p.marker, p.line); err != nil {
			fatal(err)
		}
	}
	if err := appendRouterFunc(name, lower); err != nil {
		fatal(err)
	}
	fmt.Println("✓ patched internal/router/router.go")

	// ---- 5. gofmt + 锚点回检 + 语法回检 ----
	// 调 gofmt 二进制而不是 `go fmt`：`go fmt` 接受 package 路径，跨目录
	// 文件传给它会报"named files must all be in one directory"。
	if err := runGofmt("internal/server.go", "internal/router/router.go"); err != nil {
		fatal(fmt.Errorf("gofmt: %w", err))
	}
	for _, m := range markers {
		if err := requireMarker(m.file, m.name); err != nil {
			fatal(fmt.Errorf("注入后锚点丢失（脚本逻辑 bug）：%w", err))
		}
	}
	for _, f := range []string{"internal/server.go", "internal/router/router.go"} {
		if err := parseCheck(f); err != nil {
			fatal(err)
		}
	}

	// ---- 6. 打印剩余手工步骤 ----
	fmt.Print(renderNextSteps(name, lower))
}

// ---------------------------------------------------------------------------
// 文件 / 替换 helpers

// copyAndRename 把 src 复制到 dst，并把全文里 Example→Name / example→lower
// 替换掉。两次 replaceAll 的顺序无所谓：Name 已经是大写开头，与 example
// 的全小写互不干扰。
func copyAndRename(src, dst, name, lower string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	body := strings.ReplaceAll(string(b), "Example", name)
	body = strings.ReplaceAll(body, "example", lower)
	return os.WriteFile(dst, []byte(body), 0o644)
}

// requireMarker 校验 file 里有 `// NEH <marker>` 全行注释（允许行首空白）。
// 全行匹配避免 godoc 里提到锚点名时被误命中。
func requireMarker(file, marker string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	re := regexp.MustCompile(`(?m)^\s*// NEH ` + regexp.QuoteMeta(marker) + `$`)
	if !re.Match(b) {
		return fmt.Errorf("%s 缺锚点 '// NEH %s'，无法幂等注入", file, marker)
	}
	return nil
}

// insertBeforeMarker 在 file 里把 line 插到首个 `// NEH <marker>` 全行注释
// 之前。多次调用相同 marker 时新插入行依次堆在 marker 之前（与旧 awk 版
// 行为一致：marker 行未被消耗，下次插入也匹配同一行）。
func insertBeforeMarker(file, marker, line string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	lines := strings.Split(string(b), "\n")
	re := regexp.MustCompile(`^\s*// NEH ` + regexp.QuoteMeta(marker) + `$`)

	out := make([]string, 0, len(lines)+1)
	inserted := false
	for _, l := range lines {
		if !inserted && re.MatchString(l) {
			out = append(out, line)
			inserted = true
		}
		out = append(out, l)
	}
	if !inserted {
		return fmt.Errorf("%s: marker '// NEH %s' not matched (should have been caught by requireMarker)", file, marker)
	}
	return os.WriteFile(file, []byte(strings.Join(out, "\n")), 0o644)
}

// appendRouterFunc 在 router.go 末尾 append register<Name>Routes 函数。
func appendRouterFunc(name, lower string) error {
	const tmpl = `
// register%[1]sRoutes 挂 /%[2]ss/* 路由：默认按 example 模板生成
// 列表 / 创建 / EnqueueTask 三条。生成后按真实业务调整：删多余路由、
// 加 AuthRequired 中间件、调路径形态。
func register%[1]sRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.%[1]s == nil {
		return
	}

	%[2]ss := r.Group("/%[2]ss")
	%[2]ss.GET("", deps.%[1]s.List)
	%[2]ss.POST("", deps.%[1]s.Create)
	%[2]ss.POST("/tasks", deps.%[1]s.EnqueueTask)
}
`
	const path = "internal/router/router.go"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", path, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, tmpl, name, lower); err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	return nil
}

func writeFile(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func parseCheck(path string) error {
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, path, nil, parser.AllErrors); err != nil {
		return fmt.Errorf("注入后 parse %s 失败：%w", path, err)
	}
	return nil
}

func runGofmt(paths ...string) error {
	args := append([]string{"-w"}, paths...)
	cmd := exec.Command("gofmt", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "new-endpoint:", err)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// 测试模板

// renderHandlerTest 生成 internal/handler/<lower>_test.go 模板。
// helper 名带 ${NAME}/${LOWER} 前缀，避开与 example_test.go 同包重名冲突。
func renderHandlerTest(name, lower string) string {
	// 用 buffer + fmt.Fprintf 模板拼接而不是 text/template：模板里全是 Go
	// 代码且包含反引号，加一层模板渲染没收益、徒增转义负担。
	var b bytes.Buffer
	fmt.Fprintf(&b, `package handler

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

// mock%[1]sRepo 提供 service.%[1]sRepository 接口的可注入 mock，
// 用 func 字段而不是 if/else 让每个用例只写要关心的行为。
type mock%[1]sRepo struct {
	createFunc func(ctx context.Context, %[2]s *model.%[1]s) error
	listFunc   func(ctx context.Context, limit, offset int) ([]model.%[1]s, int64, error)
}

func (m *mock%[1]sRepo) Create(ctx context.Context, e *model.%[1]s) error {
	return m.createFunc(ctx, e)
}

func (m *mock%[1]sRepo) List(ctx context.Context, limit, offset int) ([]model.%[1]s, int64, error) {
	return m.listFunc(ctx, limit, offset)
}

type mock%[1]sQueue struct {
	available   bool
	enqueueFunc func(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

func (m *mock%[1]sQueue) Available() bool { return m.available }
func (m *mock%[1]sQueue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return m.enqueueFunc(ctx, t, opts...)
}

// setup%[1]sRouter 构造一个仅注册 /%[2]ss/* 路由的最小 gin engine，
// 替代去 internal/server.go 拉整套依赖。注入"试探性 trace_id"中间件让
// response.metadata.trace_id 测得到。
func setup%[1]sRouter(repo service.%[1]sRepository, queues ...service.%[1]sQueue) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("trace_id", "test-trace")
		c.Next()
	})

	var queue service.%[1]sQueue
	if len(queues) > 0 {
		queue = queues[0]
	}
	svc := service.New%[1]sService(repo, queue)
	h := New%[1]sHandler(svc)
	r.POST("/%[2]ss", h.Create)
	r.GET("/%[2]ss", h.List)
	r.POST("/%[2]ss/tasks", h.EnqueueTask)
	return r
}

func init() {
	// 静音业务日志，避免审计 / trace log 刷屏 stdout；validator 初始化让
	// binding 校验报错文案非空（与 example_test.go 一致）。
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
}

// Test%[1]sHandlerCreateSuccess 是 new-endpoint 生成的"smoke 模板"，
// 验证 handler → service → repo 的接通能正常返 code=0。按真实业务字段
// 调整 payload 后追加 validation / database error / 边界值用例，参考
// example_test.go 的写法。
func Test%[1]sHandlerCreateSuccess(t *testing.T) {
	repo := &mock%[1]sRepo{
		createFunc: func(_ context.Context, e *model.%[1]s) error {
			e.ID = 1
			return nil
		},
	}
	router := setup%[1]sRouter(repo)

	req := httptest.NewRequest(http.MethodPost, "/%[2]ss",
		strings.NewReader(`+"`"+`{"name":"smoke"}`+"`"+`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %%d, want 200", w.Code)
	}
	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %%d, want 0", resp.Code)
	}
}
`, name, lower)
	return b.String()
}

// renderServiceTest 生成 internal/service/<lower>_test.go 模板。
func renderServiceTest(name, lower string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, `package service

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/model"
	applog "go-skeleton/pkg/log"
)

type mock%[1]sRepo struct {
	createFunc func(ctx context.Context, %[2]s *model.%[1]s) error
	listFunc   func(ctx context.Context, limit, offset int) ([]model.%[1]s, int64, error)
}

func (m *mock%[1]sRepo) Create(ctx context.Context, e *model.%[1]s) error {
	return m.createFunc(ctx, e)
}

func (m *mock%[1]sRepo) List(ctx context.Context, limit, offset int) ([]model.%[1]s, int64, error) {
	return m.listFunc(ctx, limit, offset)
}

type mock%[1]sQueue struct {
	available   bool
	enqueueFunc func(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

func (m *mock%[1]sQueue) Available() bool { return m.available }
func (m *mock%[1]sQueue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return m.enqueueFunc(ctx, t, opts...)
}

func init() {
	applog.SetLogger(zap.NewNop())
}

// Test%[1]sServiceCreateSuccess 是 new-endpoint 生成的 smoke 模板。
// 按真实业务追加边界（重复创建 / 关联失败 / 队列不可用）、并参考
// example_test.go 的 errcode.Error 断言写法补真实错误码用例。
func Test%[1]sServiceCreateSuccess(t *testing.T) {
	repo := &mock%[1]sRepo{
		createFunc: func(_ context.Context, e *model.%[1]s) error {
			e.ID = 1
			return nil
		},
	}
	svc := New%[1]sService(repo, nil)

	got, err := svc.Create(context.Background(), &Create%[1]sReq{Name: "smoke"})
	if err != nil {
		t.Fatalf("Create: %%v", err)
	}
	if got.ID != 1 {
		t.Fatalf("ID = %%d, want 1", got.ID)
	}
	if got.Name != "smoke" {
		t.Fatalf("Name = %%q, want smoke", got.Name)
	}
}
`, name, lower)
	return b.String()
}

// renderRepositoryTest 生成 internal/repository/<lower>_test.go 模板。
func renderRepositoryTest(name, lower string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, `package repository

import (
	"context"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"go-skeleton/internal/model"
)

// %[2]sCapture 收集 GORM 回调里看到的 SQL，给断言用。DryRun 模式下
// 真正不会发到 DB，但 callback 链照走，足以验证 repo 生成的 SQL 形状。
type %[2]sCapture struct {
	createCalls int
	queries     []string
}

// new%[1]sDryRunDB 起一个 DryRun 模式的 gorm.DB，不连真实 Postgres。
// 提供独立 helper 避免和 example_test.go 的 newDryRunDB 同名冲突。
func new%[1]sDryRunDB(t *testing.T, capture *%[2]sCapture) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(postgres.Open("postgres://u:p@127.0.0.1:5432/db?sslmode=disable"), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open dry run: %%v", err)
	}
	if capture == nil {
		return db
	}

	if err := db.Callback().Create().After("gorm:create").Register("test:%[2]s_capture_create", func(tx *gorm.DB) {
		capture.createCalls++
		if tx.Statement != nil {
			capture.queries = append(capture.queries, tx.Statement.SQL.String())
		}
	}); err != nil {
		t.Fatalf("register create callback: %%v", err)
	}
	return db
}

// Test%[1]sRepositoryCreate 是 new-endpoint 生成的 smoke 模板，
// 验证 Create 至少触发了一次 INSERT。参考 example_test.go 追加
// query 形状断言（ORDER BY / LIMIT / 字段名等）。
func Test%[1]sRepositoryCreate(t *testing.T) {
	capture := &%[2]sCapture{}
	db := new%[1]sDryRunDB(t, capture)
	repo := New%[1]sRepository(db)

	if err := repo.Create(context.Background(), &model.%[1]s{Name: "smoke"}); err != nil {
		t.Fatalf("Create: %%v", err)
	}
	if capture.createCalls != 1 {
		t.Fatalf("createCalls = %%d, want 1", capture.createCalls)
	}
}
`, name, lower)
	return b.String()
}

// renderNextSteps 打印手工剩余步骤（OpenAPI yaml + APIServer 方法骨架）。
// 与旧 bash 版输出一致，让从 bash 切到 go run 的用户看不出区别。
func renderNextSteps(name, lower string) string {
	return fmt.Sprintf(`
✅ 5 个分层文件 + 测试镜像已生成，server.go / router.go 装配已注入。
   现在仓库**编译不过**——APIServer 还没实现新 endpoint 对应的
   oapi.ServerInterface 方法。下面 3 步补完后 make verify 才会绿：

────────────────────────────────────────────────────────────
1. api/openapi.yaml：加路径 + schema
────────────────────────────────────────────────────────────
   把下面这段贴进 paths（按真实业务字段改）：

   /api/v1/%[2]ss:
     get:
       operationId: list%[1]ss
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
       operationId: create%[1]s
       summary: 创建
       requestBody:
         required: true
         content:
           application/json:
             schema: { $ref: '#/components/schemas/Create%[1]sReq' }
       responses:
         '200':
           description: OK

   /api/v1/%[2]ss/tasks:
     post:
       operationId: enqueue%[1]sTask
       summary: 投递异步任务
       responses:
         '200':
           description: OK

   components.schemas 下加：
   Create%[1]sReq:
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
     %[1]s *%[1]sHandler

   方法骨架（按 oapi 生成的接口签名补 params 类型）：
     func (s *APIServer) List%[1]ss(c *gin.Context, _ oapi.List%[1]ssParams) {
         s.%[1]s.List(c)
     }
     func (s *APIServer) Create%[1]s(c *gin.Context) { s.%[1]s.Create(c) }
     func (s *APIServer) Enqueue%[1]sTask(c *gin.Context) { s.%[1]s.EnqueueTask(c) }

   internal/server.go 里 APIServer struct 实例化也要补一个：
     %[1]s: %[2]sH,

────────────────────────────────────────────────────────────
3. make verify
────────────────────────────────────────────────────────────
   通过即合 PR。验证链会跑 architecture-verify / env-verify 等所有门禁。
`, name, lower)
}
