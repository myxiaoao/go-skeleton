package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/pkg/auth"
	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
	"go-skeleton/pkg/validator"
)

func TestAuthHandlerCreateToken(t *testing.T) {
	validator.InitValidator()
	manager, err := auth.NewJWTManager(auth.JWTConfig{
		Secret: "test-secret",
		Issuer: "go-skeleton-test",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/auth/token", NewAuthHandler(manager, true).CreateToken)

	req := httptest.NewRequest(http.MethodPost, "/auth/token", strings.NewReader(`{"subject":"subject-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var body response.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Code != 0 {
		t.Fatalf("expected success code, got %d", body.Code)
	}

	data, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected response data map, got %T", body.Data)
	}
	token, ok := data["access_token"].(string)
	if !ok || token == "" {
		t.Fatalf("expected access token, got %#v", data["access_token"])
	}
	if _, err := manager.ParseToken(token); err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
}

func TestAuthHandlerCreateTokenReturnsServiceDisabledWhenManagerMissing(t *testing.T) {
	validator.InitValidator()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// nil manager simulates JWT_SECRET unset. Route must still be reachable
	// and return SERVICE_DISABLED so the OpenAPI contract (always routed)
	// holds at runtime.
	router.POST("/auth/token", NewAuthHandler(nil, true).CreateToken)

	req := httptest.NewRequest(http.MethodPost, "/auth/token",
		strings.NewReader(`{"subject":"subject-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 envelope, got %d", rec.Code)
	}

	var body response.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Code != errcode.ServiceDisabled.Code() {
		t.Fatalf("code = %d, want %d", body.Code, errcode.ServiceDisabled.Code())
	}
	if body.Reason != errcode.ServiceDisabled.Reason() {
		t.Fatalf("reason = %q, want %q", body.Reason, errcode.ServiceDisabled.Reason())
	}
}

func TestAuthHandlerCreateTokenReturnsServiceDisabledWhenGated(t *testing.T) {
	validator.InitValidator()
	manager, err := auth.NewJWTManager(auth.JWTConfig{
		Secret: "test-secret",
		Issuer: "go-skeleton-test",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/auth/token", NewAuthHandler(manager, false).CreateToken)

	req := httptest.NewRequest(http.MethodPost, "/auth/token",
		strings.NewReader(`{"subject":"subject-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 envelope, got %d", rec.Code)
	}

	var body response.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Code != errcode.ServiceDisabled.Code() {
		t.Fatalf("code = %d, want %d", body.Code, errcode.ServiceDisabled.Code())
	}
	if body.Reason != errcode.ServiceDisabled.Reason() {
		t.Fatalf("reason = %q, want %q", body.Reason, errcode.ServiceDisabled.Reason())
	}
}
