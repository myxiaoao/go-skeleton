package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegistry_MiddlewareAndHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := New("test")

	engine := gin.New()
	engine.Use(r.HTTPMiddleware())
	engine.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	engine.GET("/metrics", gin.WrapH(r.Handler()))

	// 触发一条业务请求让 collector 累积一条数据。
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ping want 200, got %d", w.Code)
	}

	// /metrics 应该返 200 + 文本格式，里面能找到自家命名空间下的 collector。
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics want 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, marker := range []string{
		"go_skeleton_test_http_requests_total",
		"go_skeleton_test_http_request_duration_seconds",
		"go_skeleton_test_http_requests_in_flight",
		// 默认 collector 标志位
		"go_goroutines",
		"process_cpu_seconds_total",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("metrics output missing %q", marker)
		}
	}
}

func TestRegistry_NotFoundLabeled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := New("test")

	engine := gin.New()
	engine.Use(r.HTTPMiddleware())
	engine.GET("/metrics", gin.WrapH(r.Handler()))

	req := httptest.NewRequest(http.MethodGet, "/this-does-not-exist", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("not-found want 404, got %d", w.Code)
	}

	// FullPath 为空时应该用 not_found 兜底，而不是写空 label（空 label 会让
	// Prometheus 抓出来的 series 名很怪）。
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `route="not_found"`) {
		t.Errorf("expected route=\"not_found\" label, got body:\n%s", w.Body.String())
	}
}
