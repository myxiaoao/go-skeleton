package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

func init() {
	applog.SetLogger(zap.NewNop())
	gin.SetMode(gin.TestMode)
}

func TestTraceLoggerReusesClientRequestID(t *testing.T) {
	router := gin.New()
	router.Use(TraceLogger(false, nil))

	var seenInHandler string
	router.GET("/x", func(c *gin.Context) {
		seenInHandler = c.GetString("trace_id")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "client-supplied-id")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "client-supplied-id" {
		t.Errorf("response X-Request-ID = %q, want client-supplied-id", got)
	}
	if seenInHandler != "client-supplied-id" {
		t.Errorf("handler trace_id = %q, want client-supplied-id", seenInHandler)
	}
}

func TestTraceLoggerGeneratesWhenMissing(t *testing.T) {
	router := gin.New()
	router.Use(TraceLogger(false, nil))
	router.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if got == "" {
		t.Fatal("response X-Request-ID is empty; middleware should generate one")
	}
	// uuid.New().String() 返回 36 字符带短横线格式；这里做廉价的形状校验，免去
	// 为一条正则去 import uuid 包。
	if len(got) != 36 {
		t.Errorf("X-Request-ID = %q (len=%d), want a UUID-shaped value", got, len(got))
	}
}

func TestTraceLoggerRejectsUnsafeRequestID(t *testing.T) {
	cases := map[string]string{
		"too long":      strings.Repeat("a", maxRequestIDLen+1),
		"control chars": "id\x00with\x1bnul",
		"spaces":        "has space",
		"json breaking": `id"}`,
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.Use(TraceLogger(false, nil))
			router.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set("X-Request-ID", id)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if got := w.Header().Get("X-Request-ID"); got == id || len(got) != 36 {
				t.Errorf("X-Request-ID = %q, want a regenerated UUID", got)
			}
		})
	}
}

func TestValidRequestIDAcceptsCommonFormats(t *testing.T) {
	for _, id := range []string{"client-supplied-id", "550e8400-e29b-41d4-a716-446655440000", "asynq:task_1.2"} {
		if !validRequestID(id) {
			t.Errorf("validRequestID(%q) = false, want true", id)
		}
	}
}
