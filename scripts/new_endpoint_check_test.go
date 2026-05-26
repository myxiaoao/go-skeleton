// new_endpoint_check_test.go 覆盖 new-endpoint-check.go（只读 drift detector）:
// 比对 yaml ↔ handler / service / router / openapi.go / server.go / router_test.go
// 六处代码端，按 [!] Missing / [~] Stale / [-] Mismatch 三档报告。
package scripts

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildNewEndpointCheck 同 buildNewEndpoint，但编译 new-endpoint-check.go。
func buildNewEndpointCheck(t *testing.T) string {
	t.Helper()
	scriptPath := filepath.Join(thisDir(t), "new-endpoint-check.go")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "new-endpoint-check")
	cmd := exec.Command("go", "build", "-o", binPath, scriptPath)
	cmd.Dir = thisDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build new-endpoint-check: %v\n%s", err, out)
	}
	return binPath
}

// checkFixture 构造一个"yaml + 代码端齐全"的最小仓库，包含 Order 资源的
// handler / service / router / openapi.go / server.go / router_test.go 全套，
// 让 new-endpoint-check 在这个 fixture 上跑应该 exit 0。
//
// 这里用项目实际的 struct 名 HTTPHandlers（不是 newEndpointFixture 用的
// 简化版 handlers），让 check 能正确匹配。也写好对应的 Order handler 方法
// 集合 / service 方法 / router 注册 / APIServer 转发 / router_test deps。
func checkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)

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
`)

	writeFile(t, filepath.Join(dir, "internal", "handler", "order.go"), `package handler

type OrderHandler struct{}

func (h *OrderHandler) List() {}
func (h *OrderHandler) Create() {}
`)

	writeFile(t, filepath.Join(dir, "internal", "service", "order.go"), `package service

type OrderService struct{}

func (s *OrderService) List() {}
func (s *OrderService) Create() {}
`)

	writeFile(t, filepath.Join(dir, "internal", "handler", "openapi.go"), `package handler

type APIServer struct {
	Order *OrderHandler
}

func (s *APIServer) ListOrders(c interface{}) {}
func (s *APIServer) CreateOrder(c interface{}) {}
`)

	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

type Dependencies struct {
	Order *struct{}
}

func registerOrderRoutes(r interface{}, deps Dependencies) {
	g := r
	_ = g
	// 模拟 g.GET("", deps.Order.List) / g.POST("", deps.Order.Create) 形态
	gCall(g).GET("", listHandler{}.List)
	gCall(g).POST("", listHandler{}.Create)
}

type listHandler struct{}

func (listHandler) List()   {}
func (listHandler) Create() {}

type ginGroup interface {
	GET(string, ...interface{})
	POST(string, ...interface{})
}

func gCall(g interface{}) ginGroup { return nil }
`)

	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type HTTPHandlers struct {
	Order *struct{}
}
`)

	writeFile(t, filepath.Join(dir, "internal", "router", "router_test.go"), `package router

// fixture: deps fixture 字段
// Order: &struct{}{}
`)

	return dir
}

// TestNewEndpointCheck_CleanFixture: 完整生成的 fixture exit 0。
func TestNewEndpointCheck_CleanFixture(t *testing.T) {
	bin := buildNewEndpointCheck(t)
	dir := checkFixture(t)

	code, out := runBinary(t, dir, bin)
	if code != 0 {
		t.Fatalf("check should be clean, got exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in output, got:\n%s", out)
	}
}

// TestNewEndpointCheck_MissingAllLayers: yaml 加了新资源、代码端没生成，
// 应报 Missing。
func TestNewEndpointCheck_MissingAllLayers(t *testing.T) {
	bin := buildNewEndpointCheck(t)
	dir := checkFixture(t)

	// 加一个 Product 资源到 yaml，但代码端啥都没生成。
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
  /api/v1/products:
    x-resource: Product
    get:
      operationId: listProducts
      responses:
        '200': { description: OK }
`)

	code, out := runBinary(t, dir, bin)
	if code == 0 {
		t.Fatalf("check should fail when Product not generated\n%s", out)
	}
	if !strings.Contains(out, "Product") {
		t.Errorf("expected Product in findings, got:\n%s", out)
	}
	if !strings.Contains(out, "[!]") {
		t.Errorf("expected Missing severity tag [!] in output, got:\n%s", out)
	}
}

// TestNewEndpointCheck_StaleRouterEntry: yaml 删了 op、router 残留对应注册，
// 应报 Stale。
func TestNewEndpointCheck_StaleRouterEntry(t *testing.T) {
	bin := buildNewEndpointCheck(t)
	dir := checkFixture(t)

	// router 多注册一条 Delete（yaml 里没有）。
	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

type Dependencies struct {
	Order *struct{}
}

func registerOrderRoutes(r interface{}, deps Dependencies) {
	gCall(r).GET("", listHandler{}.List)
	gCall(r).POST("", listHandler{}.Create)
	gCall(r).DELETE("/:id", listHandler{}.Delete)
}

type listHandler struct{}

func (listHandler) List()   {}
func (listHandler) Create() {}
func (listHandler) Delete() {}

type ginGroup interface {
	GET(string, ...interface{})
	POST(string, ...interface{})
	DELETE(string, ...interface{})
}

func gCall(g interface{}) ginGroup { return nil }
`)

	code, out := runBinary(t, dir, bin)
	if code == 0 {
		t.Fatalf("check should fail on stale router entry\n%s", out)
	}
	if !strings.Contains(out, "[~]") {
		t.Errorf("expected Stale tag [~] for orphan router entry, got:\n%s", out)
	}
	if !strings.Contains(out, "Delete") {
		t.Errorf("expected mention of orphan Delete handler, got:\n%s", out)
	}
}

// TestNewEndpointCheck_RouterPathMismatch 验证 checker 会比对 yaml path 与
// router 实际注册路径，而不只是看 verb + handler 方法名。
func TestNewEndpointCheck_RouterPathMismatch(t *testing.T) {
	bin := buildNewEndpointCheck(t)
	dir := checkFixture(t)

	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

type Dependencies struct {
	Order *struct{}
}

func registerOrderRoutes(r interface{}, deps Dependencies) {
	g := r.Group("/orders")
	g.GET("/legacy", listHandler{}.List)
	g.POST("", listHandler{}.Create)
}

type listHandler struct{}

func (listHandler) List()   {}
func (listHandler) Create() {}

type ginGroup interface {
	Group(string, ...interface{}) ginGroup
	GET(string, ...interface{})
	POST(string, ...interface{})
}
`)

	code, out := runBinary(t, dir, bin, "Order")
	if code == 0 {
		t.Fatalf("check should fail on router path mismatch\n%s", out)
	}
	if !strings.Contains(out, "注册路径为 /orders/legacy") {
		t.Errorf("expected router path mismatch diagnostic, got:\n%s", out)
	}
}

// TestNewEndpointCheck_NameFilter: 传 NAME=Order 只扫该资源；不会因为别处
// yaml 不一致而误报。
func TestNewEndpointCheck_NameFilter(t *testing.T) {
	bin := buildNewEndpointCheck(t)
	dir := checkFixture(t)

	// 加一个 Product 资源到 yaml（代码端没生成）——全扫会失败，但
	// NAME=Order 只看 Order 应仍 clean。
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
  /api/v1/products:
    x-resource: Product
    get:
      operationId: listProducts
      responses:
        '200': { description: OK }
`)

	// 全扫应失败
	code, _ := runBinary(t, dir, bin)
	if code == 0 {
		t.Fatalf("global scan should report Missing on Product")
	}
	// 单扫 Order 应 clean
	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("NAME=Order should be clean even with Product drift, got exit=%d:\n%s",
			code, out)
	}
}

// TestNewEndpointCheck_AuthGroupFromGeneratedRouter 验证 checker 能识别
// new-endpoint 真实生成的鉴权子组形态：authed := g.Group("", deps.AuthRequired)
// 后续 authed.POST(...) 应被视为 AuthRequired 路由。
func TestNewEndpointCheck_AuthGroupFromGeneratedRouter(t *testing.T) {
	genBin := buildNewEndpoint(t)
	checkBin := buildNewEndpointCheck(t)
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
      operationId: createOrder
      security:
        - bearerAuth: []
      responses:
        '200': { description: OK }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrder(c interface{})
}
`)
	writeFile(t, filepath.Join(dir, "internal", "router", "router_test.go"), `package router

func buildEngine() {
	deps := Dependencies{
		// NEH test-deps
	}
	_ = deps
}
`)

	code, out := runBinary(t, dir, genBin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}

	code, out = runBinary(t, dir, checkBin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint-check should accept generated auth group, exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected clean output, got:\n%s", out)
	}
}
