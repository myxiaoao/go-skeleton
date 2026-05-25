package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
