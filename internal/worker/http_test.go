package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockPinger 是 Pinger 的手写 mock，err 非 nil 时模拟依赖不可用。
type mockPinger struct {
	err error
}

func (m mockPinger) Ping(context.Context) error { return m.err }

type healthBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func doGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	h.ServeHTTP(w, req)
	return w
}

// TestHTTPLivezAlways200 验证 /livez 不碰依赖：Redis 挂了也返 200，避免
// 下游抖动触发 K8s 重启 worker。
func TestHTTPLivezAlways200(t *testing.T) {
	h := newHTTPHandler(nil, HealthChecks{Redis: mockPinger{err: errors.New("down")}})
	w := doGet(t, h, "/livez")
	if w.Code != http.StatusOK {
		t.Fatalf("livez code = %d, want 200", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode livez: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("livez status = %q, want ok", body["status"])
	}
}

// TestHTTPHealth 表驱动覆盖 /health 判定：Redis 必查，DB 只在配置时查，任一
// 已配置依赖失败返 503。
func TestHTTPHealth(t *testing.T) {
	down := errors.New("down")
	tests := []struct {
		name       string
		checks     HealthChecks
		wantCode   int
		wantStatus string
		wantChecks map[string]string
	}{
		{
			name:       "redis ok + db 未配置",
			checks:     HealthChecks{Redis: mockPinger{}},
			wantCode:   http.StatusOK,
			wantStatus: "ok",
			wantChecks: map[string]string{"redis": "ok", "postgres": "not_configured"},
		},
		{
			name:       "redis ok + db ok",
			checks:     HealthChecks{Redis: mockPinger{}, DB: mockPinger{}},
			wantCode:   http.StatusOK,
			wantStatus: "ok",
			wantChecks: map[string]string{"redis": "ok", "postgres": "ok"},
		},
		{
			name:       "redis 不可用 → 503",
			checks:     HealthChecks{Redis: mockPinger{err: down}},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unhealthy",
			wantChecks: map[string]string{"redis": "unavailable", "postgres": "not_configured"},
		},
		{
			name:       "redis 未配置 → 503",
			checks:     HealthChecks{},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unhealthy",
			wantChecks: map[string]string{"redis": "not_configured", "postgres": "not_configured"},
		},
		{
			name:       "db 已配置但不可用 → 503",
			checks:     HealthChecks{Redis: mockPinger{}, DB: mockPinger{err: down}},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "unhealthy",
			wantChecks: map[string]string{"redis": "ok", "postgres": "unavailable"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doGet(t, newHTTPHandler(nil, tc.checks), "/health")
			if w.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", w.Code, tc.wantCode)
			}
			var body healthBody
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode health: %v", err)
			}
			if body.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", body.Status, tc.wantStatus)
			}
			for k, v := range tc.wantChecks {
				if body.Checks[k] != v {
					t.Errorf("checks[%s] = %q, want %q", k, body.Checks[k], v)
				}
			}
		})
	}
}

// TestHTTPMetrics 验证 /metrics 转发给注入的 handler；未注入时 404。
func TestHTTPMetrics(t *testing.T) {
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("go_skeleton_worker_up 1\n"))
	})
	w := doGet(t, newHTTPHandler(metricsHandler, HealthChecks{}), "/metrics")
	if w.Code != http.StatusOK || w.Body.String() != "go_skeleton_worker_up 1\n" {
		t.Fatalf("metrics = %d %q, want 200 with injected body", w.Code, w.Body.String())
	}

	w = doGet(t, newHTTPHandler(nil, HealthChecks{}), "/metrics")
	if w.Code != http.StatusNotFound {
		t.Fatalf("metrics without handler code = %d, want 404", w.Code)
	}
}

// TestNewHTTPServerSetsTimeouts 验证构造的 http.Server 带 ReadHeaderTimeout，
// 避免 Slowloris。
func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := NewHTTPServer(":0", nil, HealthChecks{})
	if srv.Addr != ":0" {
		t.Errorf("Addr = %q, want :0", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 || srv.Handler == nil {
		t.Errorf("server not fully configured: %+v", srv)
	}
}
