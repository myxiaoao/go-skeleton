package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/config"
	"go-skeleton/internal/handler"
	"go-skeleton/internal/oapi"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/validator"
)

func init() {
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
	gin.SetMode(gin.TestMode)
}

// buildEngine 模拟 internal/server.go 的路由装配，去掉真正依赖 DB / Redis
// 的 handler。Handler 实例可以是空 struct——本测试只关心路由能不能命中，
// 不验证响应体内容。
func buildEngine(t *testing.T, devTokenEnabled bool) *gin.Engine {
	t.Helper()

	engine := gin.New()
	// 兜底 handler 因缺 DB / Redis 触发的 panic。本测试只在意路由可达——拿到
	// 500 也算证明"不是 404"，路由是命中的。
	engine.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// /livez、/health、/openapi.json 在 server.go 里挂在 API group 之外，这里
	// 镜像同样的挂法，让 spec → route 覆盖检查诚实可靠。
	healthH := &handler.HealthHandler{}
	engine.GET("/livez", healthH.Live)
	engine.GET("/health", healthH.Health)
	engine.GET("/openapi.json", handler.NewOpenAPIHandler(config.DocsConfig{Theme: "system", Layout: "sidebar"}).Spec)

	api := engine.Group("/api/v1")

	authRequired := func(c *gin.Context) {
		// 模拟 BearerAuth：没 Authorization 头就返 401。
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}

	deps := Dependencies{
		Auth:         &handler.AuthHandler{DevTokenAvailable: devTokenEnabled},
		AuthRequired: authRequired,
		Example:      &handler.ExampleHandler{},
	}
	if err := RegisterRoutes(api, deps); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return engine
}

type opEntry struct {
	method string
	path   string
}

// specOperations 遍历 embed 的 OpenAPI spec，把每条操作返成 (method, path)
// 元组。用于验证 spec 里声明的路径都在路由表注册了。
func specOperations(t *testing.T) []opEntry {
	t.Helper()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}

	var ops []opEntry
	for path, item := range spec.Paths.Map() {
		if item.Get != nil {
			ops = append(ops, opEntry{http.MethodGet, path})
		}
		if item.Post != nil {
			ops = append(ops, opEntry{http.MethodPost, path})
		}
		if item.Put != nil {
			ops = append(ops, opEntry{http.MethodPut, path})
		}
		if item.Patch != nil {
			ops = append(ops, opEntry{http.MethodPatch, path})
		}
		if item.Delete != nil {
			ops = append(ops, opEntry{http.MethodDelete, path})
		}
	}
	return ops
}

func TestRouterCoversAllSpecOperations(t *testing.T) {
	engine := buildEngine(t, true) // 打开 dev token 才能覆盖 spec 里全部端点
	ops := specOperations(t)
	if len(ops) == 0 {
		t.Fatal("spec yielded zero operations; check oapi.GetSpec")
	}

	for _, op := range ops {
		t.Run(op.method+" "+op.path, func(t *testing.T) {
			req := httptest.NewRequest(op.method, op.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			// 塞一个占位 token，让 authRequired 中间件放行。
			req.Header.Set("Authorization", "Bearer stub")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Fatalf("spec lists %s %s but router returned 404 (response body=%s)",
					op.method, op.path, w.Body.String())
			}
		})
	}
}

func TestDevTokenEndpointReturnsServiceDisabledWhenOff(t *testing.T) {
	// 路由**始终**注册（与 OpenAPI spec 对齐）。dev-token 关掉时 handler 返
	// 明确的 SERVICE_DISABLED 信封，而不是 404，让前端能区分"端点没了"和
	// "端点关了"两种状态。
	engine := buildEngine(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token",
		strings.NewReader(`{"subject":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 envelope, got %d (body=%s)", w.Code, w.Body.String())
	}
	// 解响应信封但不 import pkg/response，避免循环依赖。
	if body := w.Body.String(); !strings.Contains(body, "SERVICE_DISABLED") {
		t.Fatalf("expected SERVICE_DISABLED reason in body, got %s", body)
	}
}

func TestProtectedRouteRequiresBearer(t *testing.T) {
	engine := buildEngine(t, true)

	// /api/v1/auth/me 在 spec 里声明了 security: bearerAuth → 没 token 必须 401。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without Authorization header, got %d", w.Code)
	}
}
