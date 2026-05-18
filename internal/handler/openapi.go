package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
)

// APIServer 把项目里各资源 handler 适配成 api/openapi.yaml 生成的
// oapi.ServerInterface。每个方法只是对真实 handler 的薄转发；方法签名
// 必须严格匹配 oapi.ServerInterface，文件末尾的编译期断言保证 yaml 和
// 代码漂移时 build 直接失败。
type APIServer struct {
	Auth    *AuthHandler
	Health  *HealthHandler
	Example *ExampleHandler
	OpenAPI *OpenAPIHandler
}

// 编译期保险线：APIServer 必须满足 oapi 生成的 ServerInterface 契约。
// 这行一旦编译失败，说明改了 api/openapi.yaml 但没同步 handler，跑
// `make oapi` 重新生成并补齐签名即可。
var _ oapi.ServerInterface = (*APIServer)(nil)

// GetHealth 实现 oapi.ServerInterface。
func (s *APIServer) GetHealth(c *gin.Context) {
	s.Health.Health(c)
}

// GetLivez 实现 oapi.ServerInterface。
func (s *APIServer) GetLivez(c *gin.Context) {
	s.Health.Live(c)
}

// CreateAuthToken 实现 oapi.ServerInterface。
func (s *APIServer) CreateAuthToken(c *gin.Context) {
	s.Auth.CreateToken(c)
}

// GetAuthMe 实现 oapi.ServerInterface。
func (s *APIServer) GetAuthMe(c *gin.Context) {
	s.Auth.Me(c)
}

// ListExamples 实现 oapi.ServerInterface。oapi 生成的 params 这里忽略——
// 底层 handler 会通过 ShouldBindQuery 重新绑定，让校验链路统一。
func (s *APIServer) ListExamples(c *gin.Context, _ oapi.ListExamplesParams) {
	s.Example.List(c)
}

// CreateExample 实现 oapi.ServerInterface。
func (s *APIServer) CreateExample(c *gin.Context) {
	s.Example.Create(c)
}

// EnqueueExampleTask 实现 oapi.ServerInterface。
func (s *APIServer) EnqueueExampleTask(c *gin.Context) {
	s.Example.EnqueueTask(c)
}

// GetOpenAPISpec 实现 oapi.ServerInterface。
func (s *APIServer) GetOpenAPISpec(c *gin.Context) {
	s.OpenAPI.Spec(c)
}

// OpenAPIHandler 把 embed 进二进制的 OpenAPI 3.1 spec 以 JSON 暴露给客户端，
// 用于前端导入 Postman / Bruno / Insomnia 等工具。
type OpenAPIHandler struct{}

// NewOpenAPIHandler 构造 OpenAPIHandler。无配置，无依赖。
func NewOpenAPIHandler() *OpenAPIHandler {
	return &OpenAPIHandler{}
}

// Spec 把内嵌的 OpenAPI 3.1 spec 以 application/json 返回。
func (h *OpenAPIHandler) Spec(c *gin.Context) {
	raw, err := oapi.GetSpecJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load openapi spec"})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}
