package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newSecurityHeadersTestEngine(enabled bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(SecurityHeaders(enabled))
	engine.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return engine
}

func TestSecurityHeadersWritesStandardHeadersWhenEnabled(t *testing.T) {
	engine := newSecurityHeadersTestEngine(true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	engine.ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Fatalf("header %s = %q, want %q", k, got, v)
		}
	}
}

func TestSecurityHeadersSkipsWhenDisabled(t *testing.T) {
	engine := newSecurityHeadersTestEngine(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	engine.ServeHTTP(rec, req)

	for _, k := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if got := rec.Header().Get(k); got != "" {
			t.Fatalf("header %s expected empty when disabled, got %q", k, got)
		}
	}
}
