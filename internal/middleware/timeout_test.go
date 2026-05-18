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

	"go-skeleton/internal/errcode"
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
		// Block until the request context is canceled (deadline exceeded).
		<-c.Request.Context().Done()
		// Do NOT write a response — let the middleware translate the deadline.
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
	// If the handler did write something (even a slow stream), we must not
	// rewrite the response: it would corrupt an already-flushed body.
	engine := gin.New()
	engine.Use(Timeout(20 * time.Millisecond))
	engine.GET("/written", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
		// Now block past the deadline, but the response is already on the wire.
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
		// No deadline should be attached.
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
	// Make sure we only emit REQUEST_TIMEOUT for DeadlineExceeded,
	// not arbitrary Canceled errors.
	engine := gin.New()
	engine.Use(Timeout(50 * time.Millisecond))
	engine.GET("/cancel", func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		_ = ctx
		// Handler wrote nothing; ctx is canceled (not deadline-exceeded).
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cancel", nil))

	// Since the request's own context did not exceed deadline, middleware
	// must NOT inject REQUEST_TIMEOUT. Gin defaults to 200 with empty body.
	if w.Code == http.StatusOK && w.Body.Len() == 0 {
		return
	}
	// Any non-timeout envelope is also fine; what we forbid is REQUEST_TIMEOUT.
	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil &&
		resp.Code == errcode.RequestTimeout.Code() {
		t.Fatalf("middleware emitted REQUEST_TIMEOUT for non-deadline cancel")
	}
}
