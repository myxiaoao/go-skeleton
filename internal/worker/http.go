package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go-skeleton/pkg/buildinfo"
)

// Pinger 是 /health 探测依赖的最小契约；生产实现是 *taskqueue.Queue（Redis）
// 与 *database.DBManager（Postgres）。
type Pinger interface {
	Ping(context.Context) error
}

// HealthChecks 收拢 worker /health 要探测的依赖。Redis 是 worker 的必需依赖
// （nil 视为不健康）；DB 可选，nil 表示未配置、不参与判定。
type HealthChecks struct {
	Redis Pinger
	DB    Pinger
}

// 依赖状态取值与 API /health 的 checks 保持同一套字面量，方便看板统一。
const (
	checkOK            = "ok"
	checkUnavailable   = "unavailable"
	checkNotConfigured = "not_configured"
	healthProbeTimeout = 2 * time.Second
)

// NewHTTPServer 构造 worker 可观测端口的 http.Server：/metrics、/livez、/health。
// 故意用标准库 net/http 而不是 gin：worker 只需要三个只读端点，不值得拉入
// gin 引擎和 API 的 handler / 中间件链；响应也不走业务信封（给探针和
// Prometheus 用）。metrics 为 nil 时不注册 /metrics。
func NewHTTPServer(addr string, metrics http.Handler, checks HealthChecks) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           newHTTPHandler(metrics, checks),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    1 << 14,
	}
}

func newHTTPHandler(metrics http.Handler, checks HealthChecks) http.Handler {
	mux := http.NewServeMux()
	if metrics != nil {
		mux.Handle("GET /metrics", metrics)
	}
	// liveness：只要进程能响应就 200。不碰依赖——Redis 抖动时重启 worker
	// 救不回 Redis，反而打断正在跑的任务。
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": buildinfo.Version})
	})
	mux.HandleFunc("GET /health", checks.serveHealth)
	return mux
}

// serveHealth 是 readiness：Redis 必查、DB 配置时才查，任一失败返 503。
//
// 注意：Redis 与 DB 是**顺序**探测、共用同一个 healthProbeTimeout（2s）。Redis
// 很慢时会吃掉大部分预算，DB 可能因 ctx 超时被标成 unavailable——此时 checks
// 里的 postgres 状态不一定是 DB 本身的问题，但整体结果都是 503，判定不受影响。
func (hc HealthChecks) serveHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthProbeTimeout)
	defer cancel()

	redis := probe(ctx, hc.Redis)
	db := probe(ctx, hc.DB)
	healthy := redis == checkOK && db != checkUnavailable

	status, code := "ok", http.StatusOK
	if !healthy {
		status, code = "unhealthy", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"status": status,
		"checks": map[string]string{"redis": redis, "postgres": db},
	})
}

// probe 把单个依赖的 Ping 结果翻译成状态字面量；p 为 nil 视为未配置。
func probe(ctx context.Context, p Pinger) string {
	switch {
	case p == nil:
		return checkNotConfigured
	case p.Ping(ctx) != nil:
		return checkUnavailable
	default:
		return checkOK
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
