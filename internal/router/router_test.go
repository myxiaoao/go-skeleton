package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

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

// buildEngine wires the routes the same way internal/server.go does, minus
// the DB/Redis-dependent handlers. Handler instances may be empty structs
// because we only care about routing, not response bodies.
func buildEngine(t *testing.T, devTokenEnabled bool) *gin.Engine {
	t.Helper()

	engine := gin.New()
	// Catch panics from handlers that lack real DB/Redis dependencies. We only
	// care that the route is reachable — a 500 still proves "not 404".
	engine.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// /health and /openapi.json are wired outside the API group in server.go.
	engine.GET("/health", (&handler.HealthHandler{}).Health)
	engine.GET("/openapi.json", handler.NewOpenAPIHandler().Spec)

	api := engine.Group("/api/v1")

	authRequired := func(c *gin.Context) {
		// stand-in BearerAuth: 401 when no Authorization header.
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

// specOperations walks the embedded OpenAPI spec and returns (method, path)
// tuples for every declared operation.
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
	engine := buildEngine(t, true) // enable dev token to cover full spec
	ops := specOperations(t)
	if len(ops) == 0 {
		t.Fatal("spec yielded zero operations; check oapi.GetSpec")
	}

	for _, op := range ops {
		t.Run(op.method+" "+op.path, func(t *testing.T) {
			req := httptest.NewRequest(op.method, op.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			// Pass a stand-in token so authRequired middleware lets us through.
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
	// Route is ALWAYS registered (matches OpenAPI spec). When the dev token
	// feature is off the handler returns a clear SERVICE_DISABLED envelope,
	// not 404, so clients can distinguish "endpoint gone" from "off".
	engine := buildEngine(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token",
		strings.NewReader(`{"subject":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 envelope, got %d (body=%s)", w.Code, w.Body.String())
	}
	// Decode the envelope without importing pkg/response (avoid cycles).
	if body := w.Body.String(); !strings.Contains(body, "SERVICE_DISABLED") {
		t.Fatalf("expected SERVICE_DISABLED reason in body, got %s", body)
	}
}

func TestProtectedRouteRequiresBearer(t *testing.T) {
	engine := buildEngine(t, true)

	// /api/v1/auth/me declares security: bearerAuth in spec → must 401 sans token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without Authorization header, got %d", w.Code)
	}
}
