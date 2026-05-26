// new_endpoint_dto_test.go 覆盖 new-endpoint --dto / DTO=1 模式:
// 从 yaml schema 反推请求 DTO struct（含 binding tag）+ handler 自动
// ShouldBindJSON / ShouldBindQuery + service 签名同步换成 (ctx, *XxxReq)。
// 遇到 allOf / oneOf / anyOf / 复杂 schema 时降级到空 struct + // TODO。
package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dtoYAMLFixture 写入 yaml + oapi 产物，给后面所有 DTO 测试复用。
// yaml 含两条 op：listOrders（query params） / createOrder（requestBody）。
func dtoYAMLFixture(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    x-resource: Order
    get:
      operationId: listOrders
      parameters:
        - in: query
          name: limit
          schema: { type: integer, minimum: 1, maximum: 100 }
        - in: query
          name: offset
          schema: { type: integer, minimum: 0 }
      responses:
        '200': { description: OK }
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name:
                  type: string
                  minLength: 1
                  maxLength: 255
                qty:
                  type: integer
                  minimum: 1
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrder(c interface{})
}
`)
}

// TestNewEndpoint_DTODefaultOff: 不传 --dto / DTO=1 时维持原行为——service
// 方法签名不带 *Req，handler 不生成 ShouldBind。这条是 DTO=0 基线，保护
// 现有调用方不被一刀切影响。
func TestNewEndpoint_DTODefaultOff(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)
	dtoYAMLFixture(t, dir)

	code, out := runBinary(t, dir, bin, "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}
	service, err := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(service), "CreateOrderReq") {
		t.Errorf("DTO=0 should NOT generate CreateOrderReq, got:\n%s", service)
	}
	handler, _ := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if strings.Contains(string(handler), "ShouldBindJSON") {
		t.Errorf("DTO=0 should NOT use ShouldBindJSON, got:\n%s", handler)
	}
}

// TestNewEndpoint_DTORequestBody: --dto 模式从 requestBody 生成 CreateOrderReq
// + ShouldBindJSON。binding tag 含 required + min + max。
func TestNewEndpoint_DTORequestBody(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)
	dtoYAMLFixture(t, dir)

	code, out := runBinary(t, dir, bin, "--dto", "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}

	service, err := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"type CreateOrderReq struct",
		"Name string",
		`json:"name" binding:"required,min=1,max=255"`,
		"Qty int",
		`json:"qty" binding:"min=1"`,
		"func (s *OrderService) Create(ctx context.Context, req *CreateOrderReq)",
	}
	for _, w := range want {
		if !strings.Contains(string(service), w) {
			t.Errorf("service should contain %q, got:\n%s", w, service)
		}
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	wantHandler := []string{
		"var req service.CreateOrderReq",
		"c.ShouldBindJSON(&req)",
		"response.WriteValidationError(c, err)",
		"h.svc.Create(c.Request.Context(), &req)",
	}
	for _, w := range wantHandler {
		if !strings.Contains(string(handler), w) {
			t.Errorf("handler should contain %q, got:\n%s", w, handler)
		}
	}
}

// TestNewEndpoint_DTOQueryParams: --dto 模式从 in=query 参数合成 ListOrderReq
// + ShouldBindQuery；query DTO 用 form: tag（gin ShouldBindQuery 走 form/url）。
func TestNewEndpoint_DTOQueryParams(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)
	dtoYAMLFixture(t, dir)

	code, out := runBinary(t, dir, bin, "--dto", "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}

	service, _ := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	wantService := []string{
		"type ListOrderReq struct",
		`form:"limit" binding:"min=1,max=100"`,
		`form:"offset" binding:"min=0"`,
		"func (s *OrderService) List(ctx context.Context, req *ListOrderReq)",
	}
	for _, w := range wantService {
		if !strings.Contains(string(service), w) {
			t.Errorf("service should contain %q, got:\n%s", w, service)
		}
	}
	if strings.Contains(string(service), `json:"limit"`) {
		t.Errorf("query DTO 应该用 form: tag，不是 json:\n%s", service)
	}

	handler, _ := os.ReadFile(filepath.Join(dir, "internal", "handler", "order.go"))
	if !strings.Contains(string(handler), "c.ShouldBindQuery(&req)") {
		t.Errorf("handler should use ShouldBindQuery for List, got:\n%s", handler)
	}
}

// TestNewEndpoint_DTODegradeAllOf: 遇到 allOf / 复杂 schema 时降级——生成
// 空 struct + // TODO 注释，并标 reason；不阻塞其他正常 op 的生成。
func TestNewEndpoint_DTODegradeAllOf(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)
	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    x-resource: Order
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  properties:
                    name: { type: string }
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	CreateOrder(c interface{})
}
`)

	code, out := runBinary(t, dir, bin, "--dto", "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}

	service, _ := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	// 降级 struct 应该出现，含 reason 提示作者手写。
	if !strings.Contains(string(service), "type CreateOrderReq struct") {
		t.Errorf("降级仍要生成空 struct 占位，got:\n%s", service)
	}
	if !strings.Contains(string(service), "降级") || !strings.Contains(string(service), "allOf") {
		t.Errorf("降级 struct 应带 reason 注释提示作者，got:\n%s", service)
	}
}

// TestNewEndpoint_DTOEnvVar: DTO=1 env 变量与 --dto flag 等价。
func TestNewEndpoint_DTOEnvVar(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)
	dtoYAMLFixture(t, dir)

	cmd := exec.Command(bin, "Order")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DTO=1", "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("DTO=1 run failed: %v\n%s", err, buf.String())
	}
	service, _ := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if !strings.Contains(string(service), "CreateOrderReq") {
		t.Errorf("DTO=1 env 应生成 CreateOrderReq，got:\n%s", service)
	}
}

// TestNewEndpoint_DTOIdentifierNames 验证 schema / query 字段名里的 snake_case
// 和 kebab-case 会转成合法 exported Go 字段名，wire tag 保留协议原名。
func TestNewEndpoint_DTOIdentifierNames(t *testing.T) {
	bin := buildNewEndpoint(t)
	dir := minimalAnchorsFixture(t)

	writeFile(t, filepath.Join(dir, "api", "openapi.yaml"), `openapi: 3.1.0
info: { title: fixture, version: 0.1.0 }
paths:
  /api/v1/orders:
    x-resource: Order
    get:
      operationId: listOrders
      parameters:
        - in: query
          name: x-request-id
          schema: { type: string }
      responses:
        '200': { description: OK }
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                order_id: { type: string }
                2fa-enabled: { type: boolean }
      responses:
        '200': { description: OK }
`)
	writeFile(t, filepath.Join(dir, "internal", "oapi", "oapi.gen.go"), `package oapi

type ServerInterface interface {
	ListOrders(c interface{})
	CreateOrder(c interface{})
}
`)

	code, out := runBinary(t, dir, bin, "--dto", "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d:\n%s", code, out)
	}

	service, err := os.ReadFile(filepath.Join(dir, "internal", "service", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(service)
	want := []string{
		"OrderId string `json:\"order_id\"`",
		"Field2faEnabled bool `json:\"2fa-enabled\"`",
		"XRequestId string `form:\"x-request-id\"`",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("service should contain %q, got:\n%s", w, got)
		}
	}
	for _, bad := range []string{"Order_id string", "X-request-id string", "\n\t2faEnabled"} {
		if strings.Contains(got, bad) {
			t.Errorf("service should not contain invalid / unsanitized field %q:\n%s", bad, got)
		}
	}
}
