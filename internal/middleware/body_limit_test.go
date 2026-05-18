package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newBodyLimitTestEngine(limit int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(MaxBodyBytes(limit))
	engine.POST("/", func(c *gin.Context) {
		body := make([]byte, 0, 1024)
		buf := make([]byte, 256)
		for {
			n, err := c.Request.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if err != nil {
				if err.Error() == "http: request body too large" {
					c.String(http.StatusRequestEntityTooLarge, "too large")
					return
				}
				break
			}
		}
		c.String(http.StatusOK, "got %d bytes", len(body))
	})
	return engine
}

func TestMaxBodyBytesAcceptsUnderLimit(t *testing.T) {
	engine := newBodyLimitTestEngine(64)
	body := bytes.Repeat([]byte("a"), 10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
}

func TestMaxBodyBytesRejectsOverLimit(t *testing.T) {
	engine := newBodyLimitTestEngine(8)
	body := bytes.Repeat([]byte("a"), 1024)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", rec.Code, rec.Body.String())
	}
}

func TestMaxBodyBytesZeroDisables(t *testing.T) {
	engine := newBodyLimitTestEngine(0)
	body := bytes.Repeat([]byte("a"), 4096)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when limit=0; body=%q", rec.Code, rec.Body.String())
	}
}
