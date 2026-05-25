// scripts_test.go 给 scripts/ 下的 //go:build ignore 脚本加黑盒回归。
//
// 这些脚本本身是 main，没法被普通 import 调起来；用 t.TempDir 准备一个迷你
// "假仓库"，git init 后用 go run /abs/path/scripts/X.go 跑——脚本会把 cwd
// 当成仓库根来操作。比拆 testable 子包侵入小、比 mock fs 更贴真实行为。
//
// 范围只覆盖核心 parser / render 路径，不复刻所有边角 case：env-verify
// 的 helper 检测、architecture-verify 的规则 1 与 2、new-endpoint 的
// anchor 注入与"已存在拒绝覆盖"。出 bug 时这三类是最有可能漂的。
package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// containsTokens 把 src 拆成空白分隔的 token 序列，断言 tokens 按顺序出现
// 在某一行里（不要求紧邻，只要顺序）。用来跳过 gofmt 字段对齐留下的多
// 空格——直接 strings.Contains("Order *handler.OrderHandler") 会被对齐
// 后的 "Order   *handler.OrderHandler" 卡掉。
func containsTokens(src string, tokens ...string) bool {
	// 各 token 之间用 \s+ 连接，行内匹配（不跨行）。
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = regexp.QuoteMeta(t)
	}
	pattern := strings.Join(parts, `\s+`)
	return regexp.MustCompile(pattern).MatchString(src)
}

// thisDir 返回本测试文件所在目录的绝对路径（即 scripts/）。脚本路径由它拼出来。
func thisDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(self)
}

// runScript 在 workdir 里跑脚本。两种模式：
//  1. 如果脚本只 import 标准库（env-verify / architecture-verify / docs-verify），
//     直接 `go run /abs/path/script.go`——workdir 不需要 go.mod。
//  2. 如果脚本 import 第三方库（new-endpoint 用到 kin-openapi），先在主仓库
//     用 `go build` 编出 binary（带主 go.mod 的依赖解析），再在 workdir 跑
//     这个 binary——workdir 不需要 go.mod。
//
// runScript 默认走模式 1。new-endpoint 测试自己用 runBinary 走模式 2。
func runScript(t *testing.T, workdir, script string, args ...string) (int, string) {
	t.Helper()
	scriptPath := filepath.Join(thisDir(t), script)
	cmdArgs := append([]string{"run", scriptPath}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = workdir
	// 继承宿主 PATH / GOROOT 等关键 env；其余清掉避免污染。
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err == nil {
		return 0, out
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out
	}
	t.Fatalf("exec %s: %v\noutput:\n%s", script, err, out)
	return -1, out
}

// buildNewEndpoint 在主仓库 cwd 下用 go build 把 scripts/new-endpoint.go 编
// 成 tmp 可执行 binary，返回 binary 路径。共享给所有 new-endpoint 测试用例，
// 避免每个 case 重编一次。Cleanup 在 binary 与 t.TempDir 同生命周期。
func buildNewEndpoint(t *testing.T) string {
	t.Helper()
	scriptPath := filepath.Join(thisDir(t), "new-endpoint.go")
	// 在 scripts/ 同目录 build：这样 go 能找到主仓库的 go.mod。
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "new-endpoint")
	cmd := exec.Command("go", "build", "-o", binPath, scriptPath)
	cmd.Dir = thisDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build new-endpoint: %v\n%s", err, out)
	}
	return binPath
}

// runBinary 在 workdir 跑 binary 加 args，返回 exit code + combined output。
func runBinary(t *testing.T, workdir, binPath string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err == nil {
		return 0, out
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out
	}
	t.Fatalf("exec %s: %v\noutput:\n%s", binPath, err, out)
	return -1, out
}

// initRepo 在 dir 下 git init + 配 user.email / user.name + 一次空 commit，
// 让 git rev-parse --show-toplevel 能拿到 dir。脚本里 repoRoot() 全靠它。
func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "ci@example.com")
	run("config", "user.name", "ci")
}

// writeFile 是 t.TempDir 子树的便利写入。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// env-verify

func TestEnvVerify_HappyPath(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

func Load() {
	_ = os.Getenv("APP_ENV")
	_ = intEnv("HTTP_PORT", 3000)
}

func intEnv(key string, def int) int { _ = key; return def }
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `APP_ENV=development
HTTP_PORT=3000
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code != 0 {
		t.Fatalf("env-verify exit=%d, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "2 keys") {
		t.Errorf("expected '2 keys' in output, got:\n%s", out)
	}
}

func TestEnvVerify_MissingInExample(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// config 读了 HTTP_PORT，模板里没列。脚本应在 stderr 指出 HTTP_PORT 缺失。
	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

func Load() {
	_ = os.Getenv("HTTP_PORT")
}
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `APP_ENV=development
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code == 0 {
		t.Fatalf("env-verify should fail when .env.example missing keys\n%s", out)
	}
	if !strings.Contains(out, "HTTP_PORT") {
		t.Errorf("expected HTTP_PORT in diagnostic, got:\n%s", out)
	}
}

// 注释 / 字符串字面量里出现 KEY 字样不应被命中——AST 走 CallExpr 第一参数
// 的 BasicLit，不会扫到 godoc 或日志里的 "POSTGRES" 字样。
func TestEnvVerify_IgnoresCommentsAndOtherStrings(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

// 这个注释里提到 GHOST_KEY 不应被识别为 env key。
const note = "GHOST_KEY also not a key"

func Load() {
	_ = os.Getenv("REAL_KEY")
}
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `REAL_KEY=
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code != 0 {
		t.Fatalf("env-verify exit=%d, expected 0\n%s", code, out)
	}
	if strings.Contains(out, "GHOST_KEY") {
		t.Errorf("GHOST_KEY should not be detected, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// architecture-verify

func TestArchitectureVerify_GinInService(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 1 的命中文件：service 包不能 import gin。
	writeFile(t, filepath.Join(dir, "internal", "service", "x.go"), `package service

import "github.com/gin-gonic/gin"

var _ = gin.Default
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when service imports gin\n%s", out)
	}
	if !strings.Contains(out, "rule 1") || !strings.Contains(out, "github.com/gin-gonic/gin") {
		t.Errorf("expected rule 1 + gin in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/service/x.go") {
		t.Errorf("expected service/x.go in diagnostic, got:\n%s", out)
	}
}

func TestArchitectureVerify_GormOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 2 的命中：handler 包不能 import gorm（只允许 repository / model /
	// bootstrap / pkg/database）。
	writeFile(t, filepath.Join(dir, "internal", "handler", "x.go"), `package handler

import "gorm.io/gorm"

var _ = gorm.DB{}
`)
	// 反例：repository 包 import gorm，不应被告警。
	writeFile(t, filepath.Join(dir, "internal", "repository", "ok.go"), `package repository

import "gorm.io/gorm"

var _ = gorm.DB{}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when handler imports gorm\n%s", out)
	}
	if !strings.Contains(out, "rule 2") {
		t.Errorf("expected rule 2 in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/handler/x.go") {
		t.Errorf("expected handler/x.go violation, got:\n%s", out)
	}
	if strings.Contains(out, "internal/repository/ok.go") {
		t.Errorf("repository/ok.go is in allow list, should not be flagged:\n%s", out)
	}
}

func TestArchitectureVerify_Clean(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 空仓库：service / repository / pkg 都没文件，4 条规则都应通过。
	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify exit=%d on empty repo, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in success output, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// new-endpoint
//
// 改造后 new-endpoint 从 api/openapi.yaml + internal/oapi/oapi.gen.go 反向
// 驱动。fixture 要同时准备这两个文件 + 5 个锚点宿主（server.go / router.go /
// openapi.go）。脚本依赖 kin-openapi 第三方库，所以测试走 binary 模式（先在
// 主仓库 go build，再在 tmp workdir 跑 binary）。

// newEndpointFixture 准备一个最小可注入的"假仓库"：
//   - api/openapi.yaml 含 Order 资源的 3 个 operation（list/create/get）
//   - internal/oapi/oapi.gen.go 含对应 ServerInterface 方法
//   - internal/server.go / router/router.go / handler/openapi.go 各带必需锚点
func newEndpointFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)

	// 1) api/openapi.yaml —— 三个 Order operation（含 path param 形态的 getOrder）
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info:
  title: fixture
  version: 0.1.0
paths:
  /api/v1/orders:
    get:
      operationId: listOrders
      responses:
        '200': { description: OK }
    post:
      operationId: createOrder
      responses:
        '200': { description: OK }
  /api/v1/orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: string }
      responses:
        '200': { description: OK }
`)

	// 2) internal/oapi/oapi.gen.go —— 模拟 oapi-codegen 已经跑过的产物
	//    （只需要 ServerInterface 接口，其它生成内容不影响脚本逻辑）。
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

// fixture: ServerInterface 模拟 oapi-codegen 产物——脚本扫这里的方法集。
type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrder(c interface{})
	GetOrder(c interface{}, id string)
}
`)

	// 3) server.go / router.go / handler/openapi.go —— 三处锚点宿主。
	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type handlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *handlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &handlers{
		// NEH handlers-return
	}
}
`)

	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

import "github.com/gin-gonic/gin"

type Dependencies struct {
	// NEH deps-fields
}

func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	// NEH routes-register
	return nil
}
`)

	writeFile(t, filepath.Join(dir, "internal", "handler", "openapi.go"), `package handler

type APIServer struct {
	// NEH apiserver-fields
}

// NEH apiserver-methods
`)

	return dir
}

func TestNewEndpoint_InjectsAnchors(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := newEndpointFixture(t)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	// 生成产物：5 个分层文件 + 3 个测试。
	for _, p := range []string{
		"internal/handler/order.go",
		"internal/service/order.go",
		"internal/repository/order.go",
		"internal/model/order.go",
		"internal/task/order.go",
		"internal/handler/order_test.go",
		"internal/service/order_test.go",
		"internal/repository/order_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected file generated: %s\n%s", p, out)
		}
	}

	// handler/order.go 应有 3 个方法（List/Create/Get）——按 yaml 反推。
	handlerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	handler := string(handlerBytes)
	for _, want := range []string{
		"func (h *OrderHandler) List(",
		"func (h *OrderHandler) Create(",
		"func (h *OrderHandler) Get(",
		`c.Param("id")`,
	} {
		if !strings.Contains(handler, want) {
			t.Errorf("handler/order.go missing %q\n--- file ---\n%s", want, handler)
		}
	}

	// service/order.go 应返 errcode.NotImplementedYet。
	serviceBytes, err := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	service := string(serviceBytes)
	if !strings.Contains(service, "errcode.NotImplementedYet") {
		t.Errorf("service/order.go missing NotImplementedYet placeholder")
	}

	// server.go 装配。
	serverBytes, err := os.ReadFile(filepath.Join(dir, "internal", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	server := string(serverBytes)
	if !containsTokens(server, "Order", "*handler.OrderHandler") {
		t.Errorf("server.go missing Order field\n%s", server)
	}
	for _, want := range []string{
		"orderRepository := repository.NewOrderRepository",
		"orderService := service.NewOrderService",
		"orderH := handler.NewOrderHandler",
		"// NEH handlers-fields",
		"// NEH handlers-return",
	} {
		if !strings.Contains(server, want) {
			t.Errorf("server.go missing %q after injection", want)
		}
	}

	// router.go —— 字段 + register 调用 + 末尾 register<Name>Routes 函数 +
	// 按 yaml verb 推的 3 条路由。
	routerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router.go"))
	if err != nil {
		t.Fatal(err)
	}
	router := string(routerBytes)
	for _, want := range []string{
		"registerOrderRoutes(r, deps)",
		"func registerOrderRoutes(",
		`g.GET("", deps.Order.List)`,
		`g.POST("", deps.Order.Create)`,
		`g.GET("/:id", deps.Order.Get)`,
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router.go missing %q\n%s", want, router)
		}
	}

	// handler/openapi.go —— APIServer 字段 + 3 个转发方法。
	apiBytes, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "openapi.go"))
	if err != nil {
		t.Fatal(err)
	}
	api := string(apiBytes)
	for _, want := range []string{
		"func (s *APIServer) ListOrders(",
		"func (s *APIServer) CreateOrder(",
		"func (s *APIServer) GetOrder(",
		"s.Order.List(c)",
		"s.Order.Create(c)",
		"s.Order.Get(c)",
	} {
		if !strings.Contains(api, want) {
			t.Errorf("handler/openapi.go missing %q\n%s", want, api)
		}
	}
}

func TestNewEndpoint_RejectsMissingInYAML(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := newEndpointFixture(t)

	// NAME 在 yaml 里没有对应 operation，应被 fail-fast 拒绝。
	code, out := runBinary(t, dir, bin, "Phantom")
	if code == 0 {
		t.Fatalf("expected fail when yaml has no Phantom operations\n%s", out)
	}
	if !strings.Contains(out, "找不到") {
		t.Errorf("expected '找不到' in error, got:\n%s", out)
	}
}

func TestNewEndpoint_RejectsDuplicate(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := newEndpointFixture(t)

	// 第一次成功。
	if code, out := runBinary(t, dir, bin, "Order"); code != 0 {
		t.Fatalf("first run exit=%d, expected 0\n%s", code, out)
	}
	// 第二次同名应被预检查拦截。
	code, out := runBinary(t, dir, bin, "Order")
	if code == 0 {
		t.Fatalf("second run should fail (file exists)\n%s", out)
	}
	if !strings.Contains(out, "已存在") {
		t.Errorf("expected '已存在' in error, got:\n%s", out)
	}
}

func TestNewEndpoint_RejectsBadName(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := newEndpointFixture(t)

	// 小写起头、非 CamelCase 形态，应被 camelCaseRe 拒。
	code, out := runBinary(t, dir, bin, "order")
	if code == 0 {
		t.Fatalf("expected reject for lower-case name\n%s", out)
	}
	if !strings.Contains(out, "CamelCase") {
		t.Errorf("expected CamelCase complaint, got:\n%s", out)
	}
}

// TestNewEndpoint_SecurityBearerAuth 验证 yaml security 含 bearerAuth 时，
// 生成的 register<Name>Routes 函数会把这些路由放到 deps.AuthRequired 子组。
func TestNewEndpoint_SecurityBearerAuth(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := t.TempDir()
	initRepo(t, dir)

	// yaml：两个 operation——createOrder 公开、getOrder 要鉴权。
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    post:
      operationId: createOrder
      responses:
        '200': { description: OK }
  /api/v1/orders/{id}:
    get:
      operationId: getOrder
      security:
        - bearerAuth: []
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: string }
      responses:
        '200': { description: OK }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	CreateOrder(c interface{})
	GetOrder(c interface{}, id string)
}
`)
	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type handlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *handlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &handlers{
		// NEH handlers-return
	}
}
`)
	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

import "github.com/gin-gonic/gin"

type Dependencies struct {
	AuthRequired gin.HandlerFunc
	// NEH deps-fields
}

func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	// NEH routes-register
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "internal", "handler", "openapi.go"), `package handler

type APIServer struct {
	// NEH apiserver-fields
}

// NEH apiserver-methods
`)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	routerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router.go"))
	if err != nil {
		t.Fatal(err)
	}
	router := string(routerBytes)

	// 公开路由直接挂 g 上；鉴权路由必须在 deps.AuthRequired 子组里。
	for _, want := range []string{
		`g.POST("", deps.Order.Create)`,
		`if deps.AuthRequired != nil`,
		`authed := g.Group("", deps.AuthRequired)`,
		`authed.GET("/:id", deps.Order.Get)`,
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router.go missing %q\n--- file ---\n%s", want, router)
		}
	}
	// 反例：Get 不应直接挂在 g 上。
	if strings.Contains(router, `g.GET("/:id", deps.Order.Get)`) {
		t.Errorf("router.go: getOrder should be in authed group, not on g directly\n%s", router)
	}
}

// TestNewEndpoint_XHandlerMethodOverride 验证 yaml extension 覆盖动作名：
// operationId "orderCheckout"、x-handler-method "Checkout" → handler 方法名 Checkout。
func TestNewEndpoint_XHandlerMethodOverride(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := t.TempDir()
	initRepo(t, dir)

	// 自定义 yaml：单一 operation 用 x-handler-method。
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders/checkout:
    post:
      operationId: orderCheckout
      x-handler-method: Checkout
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	OrderCheckout(c interface{})
}
`)
	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type handlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *handlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &handlers{
		// NEH handlers-return
	}
}
`)
	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

import "github.com/gin-gonic/gin"

type Dependencies struct {
	// NEH deps-fields
}

func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	// NEH routes-register
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "internal", "handler", "openapi.go"), `package handler

type APIServer struct {
	// NEH apiserver-fields
}

// NEH apiserver-methods
`)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	handlerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	handler := string(handlerBytes)
	if !strings.Contains(handler, "func (h *OrderHandler) Checkout(") {
		t.Errorf("expected method name 'Checkout' from x-handler-method override\n%s", handler)
	}
}

// minimalAnchorsFixture 准备一个只含锚点宿主的极简 fixture：server.go /
// router.go / handler/openapi.go 三个文件 + 一个空 oapi.gen.go。yaml 与
// ServerInterface 内容由 caller 自己写。
func minimalAnchorsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type handlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *handlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &handlers{
		// NEH handlers-return
	}
}
`)
	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

import "github.com/gin-gonic/gin"

type Dependencies struct {
	// NEH deps-fields
}

func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	// NEH routes-register
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "internal", "handler", "openapi.go"), `package handler

type APIServer struct {
	// NEH apiserver-fields
}

// NEH apiserver-methods
`)
	return dir
}

// TestNewEndpoint_RouterPathFromYAML 验证 register<Name>Routes 用 yaml 真实
// resource path（如 /order-items）做 r.Group，而不是写死的 "/" + lower + "s"。
// 审计 case：原版会把 /api/v1/order-items 注册成 /orderitemss → spec 404。
func TestNewEndpoint_RouterPathFromYAML(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/order-items:
    get:
      operationId: listOrderItems
      responses:
        '200': { description: OK }
    post:
      operationId: createOrderItem
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrderItems(c interface{})
	CreateOrderItem(c interface{})
}
`)

	code, out := runBinary(t, dir, bin, "OrderItem")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	routerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router.go"))
	if err != nil {
		t.Fatal(err)
	}
	router := string(routerBytes)

	if !strings.Contains(router, `g := r.Group("/order-items")`) {
		t.Errorf("expected r.Group(\"/order-items\") matching yaml path, got:\n%s", router)
	}
	// 反例：旧实现会用 /orderitems / /orderitemss。
	for _, bad := range []string{`r.Group("/orderitems")`, `r.Group("/orderitemss")`} {
		if strings.Contains(router, bad) {
			t.Errorf("router.go should not contain %q (legacy /<lower>s behavior)\n%s", bad, router)
		}
	}
}

// TestNewEndpoint_PathParamName 验证 yaml 里 {order_id} 这种 path 参数名能
// 正确传到生成的 handler / service 里。审计 case：原版写死 c.Param("id")，
// yaml 改名后 gin 路径变 /:order_id 但 handler 取 id 拿空字符串。
func TestNewEndpoint_PathParamName(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders/{order_id}:
    get:
      operationId: getOrder
      parameters:
        - in: path
          name: order_id
          required: true
          schema: { type: string }
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	GetOrder(c interface{}, order_id string)
}
`)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	// handler 用真实 path 参数名取值。
	handlerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	handler := string(handlerBytes)
	if !strings.Contains(handler, `c.Param("order_id")`) {
		t.Errorf("expected handler to call c.Param(\"order_id\"), got:\n%s", handler)
	}
	if strings.Contains(handler, `c.Param("id")`) {
		t.Errorf("handler should not fall back to c.Param(\"id\")\n%s", handler)
	}

	// service 方法签名带真实参数名。
	serviceBytes, err := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	service := string(serviceBytes)
	if !strings.Contains(service, "Get(ctx context.Context, order_id string)") {
		t.Errorf("expected service Get(ctx, order_id string), got:\n%s", service)
	}

	// router 注册的 gin 路径是 /:order_id。
	routerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router.go"))
	if err != nil {
		t.Fatal(err)
	}
	router := string(routerBytes)
	if !strings.Contains(router, `g.GET("/:order_id", deps.Order.Get)`) {
		t.Errorf("expected gin route /:order_id, got:\n%s", router)
	}
}

// TestNewEndpoint_RouterTestDepsInjected 验证 router_test.go::buildEngine 的
// deps fixture 也被注入新资源。审计 case：原版只注入 router.go，新 spec 路径
// 走 TestRouterCoversAllSpecOperations 时 404。
func TestNewEndpoint_RouterTestDepsInjected(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    get:
      operationId: listOrders
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
}
`)
	// 关键 fixture：router_test.go 含 // NEH test-deps 锚点。
	writeFile(t, filepath.Join(dir, "internal", "router", "router_test.go"), `package router

func buildEngine() {
	deps := Dependencies{
		// NEH test-deps
	}
	_ = deps
}
`)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	bs, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(bs)
	if !containsTokens(got, "Order:", "&handler.OrderHandler{},") {
		t.Errorf("router_test.go missing Order injection\n%s", got)
	}
	if !strings.Contains(got, "// NEH test-deps") {
		t.Errorf("router_test.go: anchor should be preserved after injection\n%s", got)
	}
}

// TestNewEndpoint_RouterTestDepsOptional 验证：没 router_test.go（已被
// drop-example 清理或开发者主动删除）时，脚本不报错，只跳过该注入步骤。
func TestNewEndpoint_RouterTestDepsOptional(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    get:
      operationId: listOrders
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
}
`)
	// 不创建 router_test.go——脚本应跳过这一步。

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint should still succeed when router_test.go is absent, exit=%d\n%s", code, out)
	}
}

// TestNewEndpoint_RejectsMultiPathParam 验证 path 含 >1 个 path 参数（如
// /users/{uid}/orders/{oid}）时 fail-fast——脚本只覆盖 0/1 个参数的模板。
func TestNewEndpoint_RejectsMultiPathParam(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/users/{uid}/orders/{oid}:
    get:
      operationId: getUserOrder
      parameters:
        - in: path
          name: uid
          required: true
          schema: { type: string }
        - in: path
          name: oid
          required: true
          schema: { type: string }
      responses:
        '200': { description: OK }
`)

	code, out := runBinary(t, dir, bin, "UserOrder")
	if code == 0 {
		t.Fatalf("expected fail-fast on multi path-param, got success\n%s", out)
	}
	if !strings.Contains(out, "path 参数") || !strings.Contains(out, "x-handler-method") {
		t.Errorf("expected error mentioning path 参数 + x-handler-method, got:\n%s", out)
	}
}
