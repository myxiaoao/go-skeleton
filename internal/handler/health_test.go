package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
)

// healthPingerFunc 是接口测试桩；可写 healthPingerFunc(func(ctx context.Context) error{...})
// 同时满足 healthDBPinger 和 healthCachePinger。
type healthPingerFunc func(context.Context) error

func (f healthPingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestHealthHandlerLiveReturns200WithoutDependencies(t *testing.T) {
	// /livez 即使 db / cache 都是 nil 也必须返 200——liveness 探针只回答
	// "进程还活着"，不是"下游都健康"。失败会触发 K8s 在每次 DB / Redis 抖动
	// 时重启 Pod，是错的响应。
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
//
// 响应体必须符合 OpenAPI 的 HealthResponse（status=unhealthy + checks + build），
// 不是 ad-hoc 的 {"status":"draining"}——客户端按 spec 解析这条 503 不能踩坑。
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
	if body["status"] != "unhealthy" {
		t.Errorf("status = %v, want unhealthy", body["status"])
	}
	// 契约要求 checks / build 必填，draining 响应也不能缺。
	if _, ok := body["checks"]; !ok {
		t.Error("checks field missing from draining response")
	}
	if _, ok := body["build"]; !ok {
		t.Error("build field missing from draining response")
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

func newHealthHandlerForTest(db healthDBPinger, cache healthCachePinger) *HealthHandler {
	return &HealthHandler{db: db, cache: cache}
}

// Redis 挂掉但 Postgres 健康：should 返 200 + degraded，LB 不摘流。
// 异步任务异常不应该让整个 pod 离线——业务大多走 DB，缓存掉了顶多变慢。
func TestHealthHandlerReturnsDegradedWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	dbOK := healthPingerFunc(func(context.Context) error { return nil })
	redisFail := healthPingerFunc(func(context.Context) error { return errors.New("dial timeout") })

	h := newHealthHandlerForTest(dbOK, redisFail)
	router.GET("/health", h.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded should not flap LB)", w.Code)
	}

	var body oapi.HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != oapi.HealthResponseStatusDegraded {
		t.Errorf("status = %q, want %q", body.Status, oapi.HealthResponseStatusDegraded)
	}
	if body.Checks["redis"] != oapi.HealthResponseChecksUnavailable {
		t.Errorf("redis check = %q, want unavailable", body.Checks["redis"])
	}
	if body.Checks["postgres"] != oapi.HealthResponseChecksOk {
		t.Errorf("postgres check = %q, want ok", body.Checks["postgres"])
	}
}

// Postgres 挂掉：503 + unhealthy，LB 必须摘流——DB 不可达基本所有写都会失败。
func TestHealthHandlerReturns503WhenDBDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	dbFail := healthPingerFunc(func(context.Context) error { return errors.New("conn refused") })
	redisOK := healthPingerFunc(func(context.Context) error { return nil })

	h := newHealthHandlerForTest(dbFail, redisOK)
	router.GET("/health", h.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}

	var body oapi.HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != oapi.HealthResponseStatusUnhealthy {
		t.Errorf("status = %q, want %q", body.Status, oapi.HealthResponseStatusUnhealthy)
	}
}
