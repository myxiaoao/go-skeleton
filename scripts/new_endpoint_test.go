// new_endpoint_test.go 覆盖 new-endpoint.go 的主流程:
//   - 5 层骨架 + 3 个测试模板生成
//   - server.go / router.go / handler/openapi.go 三处锚点注入
//   - yaml security / x-handler-method / x-resource 等 extension
//   - --dry-run 计划模式 / 多 path 参数 fail-fast 等边界
//
// DTO 反推（--dto / DTO=1）独立放在 new_endpoint_dto_test.go。
// 改造后 new-endpoint 从 api/openapi.yaml + internal/oapi/oapi.gen.go 反向
// 驱动；脚本依赖 kin-openapi 第三方库，所以测试走 binary 模式（先在
// 主仓库 go build，再在 tmp workdir 跑 binary）。
package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

type HTTPHandlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *HTTPHandlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &HTTPHandlers{
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

// minimalAnchorsFixture 准备一个只含锚点宿主的极简 fixture：server.go /
// router.go / handler/openapi.go 三个文件 + 一个空 oapi.gen.go。yaml 与
// ServerInterface 内容由 caller 自己写。
func minimalAnchorsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type HTTPHandlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *HTTPHandlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &HTTPHandlers{
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

type HTTPHandlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *HTTPHandlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &HTTPHandlers{
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

type HTTPHandlers struct {
	// NEH handlers-fields
}

func newHTTPHandlers() *HTTPHandlers {
	// NEH handlers-deps

	// NEH handlers-construct

	return &HTTPHandlers{
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

// TestNewEndpoint_XResourceOperationLevel 验证 operation 级 x-resource：
// /api/v1/orders/quote 的 quoteOrder 通过 x-resource: Pricing 归到 Pricing
// 资源，NAME=Order 不应命中。
func TestNewEndpoint_XResourceOperationLevel(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    post:
      operationId: createOrder
      responses:
        '200': { description: OK }
  /api/v1/orders/quote:
    post:
      operationId: quoteOrder
      x-resource: Pricing
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	CreateOrder(c interface{})
	QuoteOrder(c interface{})
}
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
	if !strings.Contains(handler, "func (h *OrderHandler) Create(") {
		t.Errorf("Order handler should contain Create, got:\n%s", handler)
	}
	// quoteOrder 显式标了 x-resource: Pricing，不应被 Order 命中——即使
	// operationId 包含 Order 字样。
	if strings.Contains(handler, "Quote(") {
		t.Errorf("Order handler should NOT contain Quote (x-resource: Pricing excludes it)\n%s", handler)
	}
}

// TestNewEndpoint_XResourcePathLevel 验证 path 级 x-resource：path 上声明
// 一次，下面所有 verb 自动继承归属。常见用法：operationId 已经含 NAME，
// path 级 x-resource 主要是为了让"operationId 不含 NAME 但仍属该资源"的
// op 也能被识别（搭配 x-handler-method 解决动作名推不出来的问题）。
func TestNewEndpoint_XResourcePathLevel(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	// path 级 x-resource: Order；operationId 标准命名（含 Order）继承默认。
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    x-resource: Order
    get:
      operationId: listOrders
      responses:
        '200': { description: OK }
    post:
      operationId: createOrder
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrder(c interface{})
}
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
	for _, want := range []string{
		"func (h *OrderHandler) List(",
		"func (h *OrderHandler) Create(",
	} {
		if !strings.Contains(handler, want) {
			t.Errorf("Order handler missing %q (path x-resource should still match)\n%s", want, handler)
		}
	}
}

// TestNewEndpoint_XResourcePathLevelWithHandlerMethod 验证 path 级 x-resource
// + operation 级 x-handler-method 组合：operationId 不含 NAME（甚至不含
// 资源关键字）时，path x-resource 把 op 拉到 NAME，x-handler-method 提供
// 动作名。这是 path 级 x-resource 的"重命名" use case。
func TestNewEndpoint_XResourcePathLevelWithHandlerMethod(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/foo:
    x-resource: Order
    get:
      operationId: listFoo
      x-handler-method: List
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListFoo(c interface{})
}
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
	if !strings.Contains(handler, "func (h *OrderHandler) List(") {
		t.Errorf("expected OrderHandler.List from x-resource + x-handler-method combo\n%s", handler)
	}
}

// TestNewEndpoint_XResourceOperationOverridesPath 验证 operation 级覆盖 path
// 级：path 默认归 Order，但其中一个 op 单独标 x-resource: Other 跳出。
func TestNewEndpoint_XResourceOperationOverridesPath(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    x-resource: Order
    get:
      operationId: listOrders
      responses:
        '200': { description: OK }
    post:
      operationId: createOrders
      x-resource: Other
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrders(c interface{})
}
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
	if !strings.Contains(handler, "func (h *OrderHandler) List(") {
		t.Errorf("Order handler should contain List (inherits path-level x-resource), got:\n%s", handler)
	}
	// createOrders 显式覆盖成 x-resource: Other，不应进 Order。
	if strings.Contains(handler, "func (h *OrderHandler) Create(") {
		t.Errorf("Order handler should NOT contain Create (overridden to x-resource: Other)\n%s", handler)
	}
}

// TestNewEndpoint_XResourceFallback 验证没声明 x-resource 时 fallback 到
// operationId 包含 NAME 的老逻辑——保持向后兼容。
func TestNewEndpoint_XResourceFallback(t *testing.T) {
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
  /api/v1/order-payments:
    get:
      operationId: listOrderPayments
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	ListOrderPayments(c interface{})
}
`)

	// NAME=Order 在 fallback 模式下会命中 listOrderPayments（这是审计指出
	// 的歧义场景）——本测试**纪念**这个已知边界，并验证文档建议（用
	// x-resource 显式声明）能解决。这里不断言"不应命中"，因为 fallback
	// 行为就是会命中；只验证 fallback 路径仍可用。
	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}
	handlerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	handler := string(handlerBytes)
	// fallback 把两个都收进来：本身就是已知的兼容代价。
	listCount := strings.Count(handler, "func (h *OrderHandler) List")
	if listCount < 1 {
		t.Errorf("expected fallback to include at least listOrders, handler:\n%s", handler)
	}
}

// TestNewEndpoint_DryRun 验证 --dry-run：解析跑通、计划打印、但不写盘也不
// patch 既有文件。
func TestNewEndpoint_DryRun(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := newEndpointFixture(t)

	// 跑 dry-run；要走 --dry-run flag（DRY_RUN env 由 runBinary 透传宿主环境，
	// 但宿主 env 一般没设，这里走 flag 更稳）。
	code, out := runBinary(t, dir, bin, "Order", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d, expected 0\n%s", code, out)
	}

	// 计划输出要点：
	for _, want := range []string{
		"[DRY-RUN]",
		"Matched 3 operation(s)",
		"GET    /api/v1/orders",
		"POST   /api/v1/orders",
		"GET    /api/v1/orders/{id}",
		"public",
		"OrderHandler.List",
		"+ internal/handler/order.go",
		"+ internal/service/order_test.go",
		"~ internal/server.go",
		"~ internal/router/router.go",
		"~ internal/handler/openapi.go",
		"Re-run without --dry-run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q\n--- output ---\n%s", want, out)
		}
	}

	// 反例：dry-run 不应该真的创建任何文件。
	if _, err := os.Stat(filepath.Join(dir, "internal", "handler", "order.go")); err == nil {
		t.Errorf("dry-run should not create internal/handler/order.go but it exists")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "service", "order.go")); err == nil {
		t.Errorf("dry-run should not create internal/service/order.go but it exists")
	}

	// 反例：dry-run 不应该 patch server.go—文件应保持 fixture 原始状态。
	serverBytes, err := os.ReadFile(filepath.Join(dir, "internal", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverBytes), "Order") {
		t.Errorf("dry-run should not patch server.go but Order field appeared")
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
