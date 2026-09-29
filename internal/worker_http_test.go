package app

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
)

// workerTestRegistry 构造 NewWorker 所需的最小 Registry：只配 Redis 地址，
// 不连真实 Redis（asynq.NewServer 构造期不建连）。
func workerTestRegistry(metricsAddr string) *bootstrap.Registry {
	return &bootstrap.Registry{Cfg: &config.Config{
		Env:    config.EnvDevelopment,
		Redis:  config.RedisConfig{Addr: "127.0.0.1:6379"},
		Worker: config.WorkerConfig{Concurrency: 1, MetricsAddr: metricsAddr},
	}}
}

// TestNewWorkerObservabilityToggle 验证 WORKER_METRICS_ADDR 为空时不构造可观测
// 端口，非空时构造并使用配置地址。
func TestNewWorkerObservabilityToggle(t *testing.T) {
	w, err := NewWorker(workerTestRegistry(""))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w.http != nil {
		t.Fatalf("http server should be nil when WORKER_METRICS_ADDR is empty")
	}

	w, err = NewWorker(workerTestRegistry("127.0.0.1:0"))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w.http == nil || w.http.Addr != "127.0.0.1:0" {
		t.Fatalf("http server = %+v, want addr 127.0.0.1:0", w.http)
	}
}

// TestListenObservabilityFailsOnBusyPort 验证端口被占时返回 error（Run 据此
// fail-fast、不发 READY）；nil server 时跳过。
func TestListenObservabilityFailsOnBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = busy.Close() }()

	w, err := NewWorker(workerTestRegistry(busy.Addr().String()))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if _, err := w.listenObservability(); err == nil {
		t.Fatal("expected listen error on busy port")
	}

	w, err = NewWorker(workerTestRegistry(""))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ln, err := w.listenObservability()
	if err != nil || ln != nil {
		t.Fatalf("disabled observability: ln=%v err=%v, want nil/nil", ln, err)
	}
}

// TestObservabilityServesAndShutsDown 端到端验证：绑定后 /livez、/metrics 可访问，
// shutdownObservability 后端口释放。
func TestObservabilityServesAndShutsDown(t *testing.T) {
	w, err := NewWorker(workerTestRegistry("127.0.0.1:0"))
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ln, err := w.listenObservability()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w.serveObservability(ln)
	base := "http://" + ln.Addr().String()

	get := func(path string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, _ := get("/livez"); code != http.StatusOK {
		t.Errorf("/livez = %d, want 200", code)
	}
	code, body := get("/metrics")
	if code != http.StatusOK || !strings.Contains(body, "go_goroutines") {
		t.Errorf("/metrics = %d, body missing go runtime metrics", code)
	}

	w.shutdownObservability(t.Context())
	if conn, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		_ = conn.Close()
		t.Error("port should be released after shutdown")
	}
}
