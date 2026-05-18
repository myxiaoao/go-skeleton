package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
)

func TestHealthHandlerLiveReturns200WithoutDependencies(t *testing.T) {
	// /livez must succeed even when db / cache are nil — the liveness
	// probe answers "process is alive", not "downstreams are healthy".
	// Failure here would cause Kubernetes to restart the pod on every
	// transient DB / Redis blip, which is the wrong response.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewHealthHandler(nil, nil, nil)
	router.GET("/livez", h.Live)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body oapi.LivenessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
}

// /health 在 draining=true 时必须返 503，让 LB 在停服窗口内摘流。
// 这样 SIGTERM 后窗口期内的请求会被 LB 转走，不再打到当前实例。
func TestHealthHandlerReturns503WhenDraining(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	draining := &atomic.Bool{}
	draining.Store(true)

	h := NewHealthHandler(nil, nil, draining)
	router.GET("/health", h.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "draining" {
		t.Errorf("status = %v, want draining", body["status"])
	}
}

// /livez 不受 draining 影响，liveness 表达"进程在跑"，draining 期间进程仍活。
// 否则 K8s 会在 graceful drain 中途重启 pod，违背"先摘流再退出"的目标。
func TestHealthHandlerLiveIgnoresDraining(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	draining := &atomic.Bool{}
	draining.Store(true)

	h := NewHealthHandler(nil, nil, draining)
	router.GET("/livez", h.Live)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when draining", w.Code)
	}
}
