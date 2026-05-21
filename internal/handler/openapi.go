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

// docsHTML 是 /docs 在线文档页：用 Stoplight Elements（纯 CDN web component）
// 渲染同域 /openapi.json。资源走外网 unpkg CDN 并锁版本，内网/离线环境打不开。
// router="hash" 让 Elements 走单页 hash 路由，不和后端路由冲突。
// 内嵌的 fetch 拦截器从 localStorage 的 go_skeleton_token 读 token，非空时给
// TryIt 发出的请求自动加 Authorization 头，保留调试时的自动鉴权便利。
const docsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/@stoplight/elements@8.4.2/styles.min.css">
  <script src="https://unpkg.com/@stoplight/elements@8.4.2/web-components.min.js"></script>
  <script>
    (function () {
      var originalFetch = window.fetch.bind(window);
      window.fetch = function (input, init) {
        init = init || {};
        var token = window.localStorage.getItem('go_skeleton_token');
        if (token) {
          var headers = new Headers(init.headers || (input instanceof Request ? input.headers : undefined));
          headers.set('Authorization', 'Bearer ' + token);
          init.headers = headers;
        }
        return originalFetch(input, init);
      };
    })();
  </script>
  <style>html,body{height:100%;margin:0}</style>
</head>
<body>
  <elements-api apiDescriptionUrl="/openapi.json" router="hash"></elements-api>
</body>
</html>`

// OpenAPIHandler 把 embed 进二进制的 OpenAPI 3.1 spec 以 JSON 暴露给客户端，
// 用于前端导入 Postman / Bruno / Insomnia 等工具；同时通过 Docs 提供基于
// Stoplight Elements 的 /docs 在线文档页。
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

// Docs 返回基于 Stoplight Elements 的在线 API 文档页（text/html）。
// 它是文档 UI、不是业务 API：和 /openapi.json、/metrics 同级挂在根路由，
// 不进 oapi.ServerInterface、不改 api/openapi.yaml，因此返回 HTML 而非业务信封。
// 渲染依赖外网 CDN，内网/离线环境只能加载 HTML 骨架、无法渲染 spec。
func (h *OpenAPIHandler) Docs(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsHTML))
}
