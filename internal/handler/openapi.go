package handler

import (
	"bytes"
	"net/http"
	"text/template"

	"github.com/gin-gonic/gin"

	"go-skeleton/config"
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

// elementsVersion 锁定 Stoplight Elements 的 CDN 版本，与 scramble 一致。
// 升级时改这一处即可（同时影响 web-components.min.js 与 styles.min.css）。
const elementsVersion = "8.4.2"

// docsTemplate 是 /docs 在线文档页模板：用 Stoplight Elements（纯 CDN
// web component）渲染同域 /openapi.json。资源走外网 unpkg CDN 并锁版本，
// 内网/离线环境打不开。
//
// 模板用 text/template（不是 html/template）：页面内嵌大段 JS，html/template
// 会把 JS 里的 < / & 等转义破坏脚本。注入值都来自受信任的启动期 env（运维配置，
// 非用户输入），且 Theme/Layout 已在 config.validate 限定为枚举，无注入面。
//
// 行为要点：
//   - router="hash"：Elements 走单页 hash 路由，不和后端路由冲突。
//   - spec 来源：运行时 fetch 同域 /openapi.json（不内联进二进制），并把
//     servers 覆盖成当前页面 origin 后喂给 Elements——这样 TryIt 始终打到
//     访问 /docs 所用的地址，本地/部署任意域名都自洽，不受 spec 里写死的
//     server 影响、也不会跨 origin 触发 CORS。
//   - fetch 拦截器：从 localStorage 的 go_skeleton_token 读 token，非空时给
//     TryIt 请求自动加 Authorization 头，保留调试时的自动鉴权便利。
//   - Theme=system 时监听 prefers-color-scheme 动态切换 data-theme；并修复
//     Elements dark 模式下代码高亮配色（见 stoplightio/elements#2188）。
var docsTemplate = template.Must(template.New("docs").Parse(`<!DOCTYPE html>
<html lang="en" data-theme="{{.Theme}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
  <meta name="color-scheme" content="{{.Theme}}">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="https://unpkg.com/@stoplight/elements@{{.Version}}/styles.min.css">
  <script src="https://unpkg.com/@stoplight/elements@{{.Version}}/web-components.min.js"></script>
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
  <style>
    html, body { margin: 0; height: 100%; }
    body { overflow-y: hidden; }
    [data-theme="dark"] .token.property { color: rgb(128, 203, 196) !important; }
    [data-theme="dark"] .token.operator { color: rgb(255, 123, 114) !important; }
    [data-theme="dark"] .token.number { color: rgb(247, 140, 108) !important; }
    [data-theme="dark"] .token.string { color: rgb(165, 214, 255) !important; }
    [data-theme="dark"] .token.boolean { color: rgb(121, 192, 255) !important; }
    [data-theme="dark"] .token.punctuation { color: #dbdbdb !important; }
  </style>
</head>
<body style="height: 100vh; overflow-y: hidden">
  <elements-api
    id="docs-api"
    router="hash"
    layout="{{.Layout}}"
    {{if .HideTryIt}}hideTryIt="true"{{end}}
    {{if .HideSchemas}}hideSchemas="true"{{end}}
    {{if .Logo}}logo="{{.Logo}}"{{end}}
  ></elements-api>
  <script>
    // 不用 apiDescriptionUrl 让 Elements 自己拉 spec，而是先 fetch 同域
    // /openapi.json，把 servers 覆盖成当前页面 origin，再喂给 Elements。
    // 这样 TryIt 始终打到"你访问 /docs 用的那个地址"——本地 localhost / 部署
    // 域名都自洽，既不依赖 spec 里写死的 server、也不会跨 origin 触发 CORS。
    (function () {
      fetch('/openapi.json', { headers: { Accept: 'application/json' } })
        .then(function (r) { return r.json(); })
        .then(function (spec) {
          spec.servers = [{ url: window.location.origin }];
          document.getElementById('docs-api').apiDescriptionDocument = spec;
        });
    })();
  </script>
  {{if eq .Theme "system"}}
  <script>
    var mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    function updateTheme(e) {
      var mode = e.matches ? 'dark' : 'light';
      document.documentElement.setAttribute('data-theme', mode);
      document.getElementsByName('color-scheme')[0].setAttribute('content', mode);
    }
    mediaQuery.addEventListener('change', updateTheme);
    updateTheme(mediaQuery);
  </script>
  {{end}}
</body>
</html>`))

// docsView 是 docsTemplate 的渲染上下文。
type docsView struct {
	Title       string
	Theme       string
	Layout      string
	Logo        string
	HideTryIt   bool
	HideSchemas bool
	Version     string
}

// OpenAPIHandler 把 embed 进二进制的 OpenAPI 3.1 spec 以 JSON 暴露给客户端，
// 用于前端导入 Postman / Bruno / Insomnia 等工具；同时通过 Docs 提供基于
// Stoplight Elements 的 /docs 在线文档页。docsPage 在构造时按启动期配置一次性
// 预渲染，请求期只是返回静态字节，零额外开销。
type OpenAPIHandler struct {
	docsPage []byte
}

// NewOpenAPIHandler 构造 OpenAPIHandler，并按 docs 配置预渲染 /docs 页面。
// 配置在启动期固定，渲染失败属编程错误（模板内置、不依赖外部输入），用 panic
// 暴露而非静默降级。
func NewOpenAPIHandler(docs config.DocsConfig) *OpenAPIHandler {
	var buf bytes.Buffer
	if err := docsTemplate.Execute(&buf, docsView{
		Title:       docs.Title,
		Theme:       docs.Theme,
		Layout:      docs.Layout,
		Logo:        docs.Logo,
		HideTryIt:   docs.HideTryIt,
		HideSchemas: docs.HideSchemas,
		Version:     elementsVersion,
	}); err != nil {
		panic("handler: render docs page: " + err.Error())
	}
	return &OpenAPIHandler{docsPage: buf.Bytes()}
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
	c.Data(http.StatusOK, "text/html; charset=utf-8", h.docsPage)
}
