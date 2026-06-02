package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/pkg/database"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestServerMetricsFailedState(t *testing.T) {
	srv := &Server{}
	if srv.MetricsFailed() {
		t.Fatal("MetricsFailed() = true before any runtime error")
	}

	srv.markMetricsFailed(http.ErrServerClosed)
	if srv.MetricsFailed() {
		t.Fatal("MetricsFailed() = true after normal server close")
	}

	srv.markMetricsFailed(errors.New("listener closed unexpectedly"))
	if !srv.MetricsFailed() {
		t.Fatal("MetricsFailed() = false after metrics runtime error")
	}
}

func testRegistryForServer(t *testing.T, metricsAddr string) *bootstrap.Registry {
	t.Helper()

	db, err := gorm.Open(postgres.Open("postgres://u:p@127.0.0.1:5432/db?sslmode=disable"), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open dry run: %v", err)
	}

	return &bootstrap.Registry{
		Cfg: &config.Config{
			Env: config.EnvDevelopment,
			Server: config.ServerConfig{
				Port:           "127.0.0.1:0",
				RequestTimeout: 30 * time.Second,
				MetricsEnabled: true,
				MetricsAddr:    metricsAddr,
			},
			Docs: config.DocsConfig{
				Title:  "API Docs",
				Theme:  "system",
				Layout: "sidebar",
			},
			Log: config.LogConfig{
				AuditEnabled: false,
			},
		},
		DB:       database.NewTestManager(db),
		Draining: &atomic.Bool{},
	}
}

// TestNewServerSplitsMetricsWhenMetricsAddrConfigured 验证 NewServer 的装配级
// 行为：METRICS_ADDR 非空时业务 engine 不再挂 /metrics，指标只由独立
// MetricsHTTP server 暴露。只测 newMetricsServer 不足以覆盖这条分流规则。
func TestNewServerSplitsMetricsWhenMetricsAddrConfigured(t *testing.T) {
	srv, err := NewServer(testRegistryForServer(t, "127.0.0.1:9090"))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.MetricsHTTP == nil {
		t.Fatal("MetricsHTTP = nil, want separate metrics server")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("business /metrics code=%d, want 404 when METRICS_ADDR is set", rec.Code)
	}

	metricsRec := httptest.NewRecorder()
	srv.MetricsHTTP.Handler.ServeHTTP(metricsRec, req)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("separate /metrics code=%d, want 200", metricsRec.Code)
	}
}

func TestNewServerMountsMetricsOnBusinessPortByDefault(t *testing.T) {
	srv, err := NewServer(testRegistryForServer(t, ""))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.MetricsHTTP != nil {
		t.Fatalf("MetricsHTTP = %#v, want nil when METRICS_ADDR is empty", srv.MetricsHTTP)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("business /metrics code=%d, want 200 by default", rec.Code)
	}
}

// TestNewMetricsServerOnlyServesMetrics 验证独立 metrics server 的 mux：
// /metrics 路由能命中、能拿到 Prometheus 格式响应；其他路径（包括根路径）
// 一律 404，避免被误当成业务端口。
func TestNewMetricsServerOnlyServesMetrics(t *testing.T) {
	stubHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# HELP test\n"))
	})

	srv := newMetricsServer("127.0.0.1:0", stubHandler)
	if srv.Addr != "127.0.0.1:0" {
		t.Fatalf("Addr = %q, want 127.0.0.1:0", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 {
		t.Fatalf("metrics server timeouts must be set, got read-header=%s read=%s write=%s",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout)
	}

	// 直接拿 Handler 走 httptest，不需要真起 listener。
	cases := []struct {
		path     string
		wantCode int
		wantBody string // 命中时响应体应包含此关键词
	}{
		{path: "/metrics", wantCode: http.StatusOK, wantBody: "# HELP"},
		{path: "/", wantCode: http.StatusNotFound},
		{path: "/livez", wantCode: http.StatusNotFound},
		{path: "/api/v1/examples", wantCode: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("path=%s code=%d, want %d", tc.path, rec.Code, tc.wantCode)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body=%q, want contains %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
