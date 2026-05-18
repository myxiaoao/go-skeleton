package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	h := NewHealthHandler(nil, nil)
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
