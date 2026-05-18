package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/response"
	"go-skeleton/pkg/validator"
)

func init() {
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
	gin.SetMode(gin.TestMode)
}

func TestTimeoutPassesThroughWhenHandlerWritesInTime(t *testing.T) {
	engine := gin.New()
	engine.Use(Timeout(50 * time.Millisecond))
	engine.GET("/ok", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestTimeoutEmitsRequestTimeoutEnvelope(t *testing.T) {
	engine := gin.New()
	engine.Use(Timeout(20 * time.Millisecond))
	engine.GET("/slow", func(c *gin.Context) {
		// 阻塞到 request ctx 被取消（达到 deadline）。
		<-c.Request.Context().Done()
		// **不要**写响应——让中间件把 deadline 转成 REQUEST_TIMEOUT 信封。
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 envelope, got %d", w.Code)
	}

	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != errcode.RequestTimeout.Code() {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.RequestTimeout.Code())
	}
	if resp.Reason != errcode.RequestTimeout.Reason() {
		t.Fatalf("reason = %q, want %q", resp.Reason, errcode.RequestTimeout.Reason())
	}
}

func TestTimeoutDoesNotOverrideHandlerResponse(t *testing.T) {
	// 如果 handler 已经写过响应（哪怕是慢流），就不能再覆盖——已经 flush
	// 的 body 被改写会导致响应体损坏。
	engine := gin.New()
	engine.Use(Timeout(20 * time.Millisecond))
	engine.GET("/written", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
		// 这里阻塞到超时；但响应已经写出去了。
		<-c.Request.Context().Done()
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/written", nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
}

func TestTimeoutDisabledWhenZero(t *testing.T) {
	engine := gin.New()
	engine.Use(Timeout(0))
	called := false
	engine.GET("/zero", func(c *gin.Context) {
		// timeout=0 时不应给 ctx 挂 deadline。
		if _, ok := c.Request.Context().Deadline(); ok {
			t.Error("expected no deadline when timeout=0")
		}
		called = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/zero", nil))

	if !called {
		t.Fatal("handler not called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestTimeoutIgnoresCancelFromOtherSources(t *testing.T) {
	// 确认中间件**只**在 DeadlineExceeded 时返 REQUEST_TIMEOUT，普通
	// Canceled error 不触发——避免上游主动取消被误报成超时。
	engine := gin.New()
	engine.Use(Timeout(50 * time.Millisecond))
	engine.GET("/cancel", func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		_ = ctx
		// handler 啥都没写；ctx 是被 cancel 而不是 deadline-exceeded。
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cancel", nil))

	// 因为 request ctx 没超 deadline，中间件**不应**注入 REQUEST_TIMEOUT。
	// Gin 默认会返 200 + 空 body。
	if w.Code == http.StatusOK && w.Body.Len() == 0 {
		return
	}
	// 任何非超时的响应信封都可接受；我们禁止的是把 REQUEST_TIMEOUT 误注入。
	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil &&
		resp.Code == errcode.RequestTimeout.Code() {
		t.Fatalf("middleware emitted REQUEST_TIMEOUT for non-deadline cancel")
	}
}
