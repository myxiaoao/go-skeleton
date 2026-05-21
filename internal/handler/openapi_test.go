package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOpenAPISpecReturnsValidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/openapi.json", NewOpenAPIHandler().Spec)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct == "" || ct[:16] != "application/json" {
		t.Fatalf("expected application/json content-type, got %q", ct)
	}

	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if v, _ := spec["openapi"].(string); v == "" {
		t.Fatalf("expected openapi field in spec, got %v", spec["openapi"])
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths object in spec, got %T", spec["paths"])
	}
	for _, p := range []string{"/health", "/api/v1/examples", "/openapi.json"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("expected path %s in spec", p)
		}
	}
}

func TestDocs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/docs", NewOpenAPIHandler().Docs)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content-type, got %q", ct)
	}

	body := w.Body.String()
	for _, want := range []string{"elements-api", "/openapi.json", "go_skeleton_token"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected docs body to contain %q", want)
		}
	}
}
