package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/repository"
	"go-skeleton/internal/service"
	"go-skeleton/internal/worker"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/metrics"
)

// observabilityShutdownTimeout 是 worker 可观测端口优雅关闭的上限：只剩探针
// 和 Prometheus scrape 这类短请求，5s 足够。
const observabilityShutdownTimeout = 5 * time.Second

// Worker 持有从 Registry 装配出来的 Asynq 异步任务运行时（server + ServeMux），
// 以及可选的可观测端口（/metrics、/livez、/health；WORKER_METRICS_ADDR 为空时为 nil）。
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	http   *http.Server
}

// NewWorker 装配异步任务 handler 和 worker 运行时。reg 不全时返 error；
// 失败不会启动 server。
func NewWorker(reg *bootstrap.Registry) (*Worker, error) {
	if err := validateWorkerRegistry(reg); err != nil {
		return nil, err
	}

	deps, err := buildWorkerDeps(reg)
	if err != nil {
		return nil, err
	}
	metricsReg, httpSrv := newWorkerObservability(reg)
	if metricsReg != nil {
		deps.Metrics = metricsReg
	}
	mux := asynq.NewServeMux()
	worker.RegisterHandlers(mux, deps)

	return &Worker{
		http: httpSrv,
		server: worker.NewServer(
			bootstrap.AsynqRedisOpt(reg.Cfg),
			worker.ServerConfig{
				Concurrency:    reg.Cfg.Worker.Concurrency,
				Queues:         reg.Cfg.Worker.Queues,
				RetryBaseDelay: reg.Cfg.Worker.RetryBaseDelay,
				RetryMaxDelay:  reg.Cfg.Worker.RetryMaxDelay,
			},
		),
		mux: mux,
	}, nil
}

// Run 启动 Asynq worker server，阻塞到 ctx 取消，然后两阶段停服。
//
// 用 Start 而不是 Run：Run = Start + 它自己的 waitForSignals + Shutdown，
// 那条内置 signal loop 会和 cmd/worker/main.go 的 signal.NotifyContext 抢
// SIGTERM，两套信号处理并存语义混乱。Start 是同步的、返回启动期 error，让我们
// 能精确地"启动成功后才发 READY=1"——这是 onReady 的关键：在 Start 返回 nil
// 之后调，Asynq 已真正进入消费态。若 Start 失败（如 Redis 不可达），直接返
// error，READY 不会发，systemd 不会被骗成"已就绪"。onReady 为 nil 时跳过。
//
// 启用了 WORKER_METRICS_ADDR 时，可观测端口在 Start 成功后同步绑定、绑定成功
// 才回调 onReady；停服顺序为 asynq Stop → Shutdown → 可观测端口 Shutdown（带超时）。
func (w *Worker) Run(ctx context.Context, onReady func()) error {
	if w == nil || w.server == nil || w.mux == nil {
		return errNilWorker
	}

	if err := w.server.Start(w.mux); err != nil {
		return fmt.Errorf("start worker server: %w", err)
	}
	// 可观测端口在 asynq 进入消费态后同步绑定：绑不上就停掉 asynq 并返 error，
	// 保证 READY 只在"消费 + 探针"全部就绪后发出。
	ln, err := w.listenObservability()
	if err != nil {
		w.server.Stop()
		w.server.Shutdown()
		return err
	}
	w.serveObservability(ln)
	if onReady != nil {
		onReady()
	}

	<-ctx.Done()

	// Asynq 官方推荐的两阶段停服：Stop 先停拉新任务，Shutdown 再等
	// in-flight 任务完成（受 Config.ShutdownTimeout 控制，默认 8s）。
	// 跳过 Stop 直接 Shutdown 会让 shutdown 窗口内新任务被拉进来又被
	// 重新调度，破坏 at-least-once 语义。
	w.server.Stop()
	w.server.Shutdown()
	// 可观测端口最后关：停服窗口内 /livez 仍可答、/metrics 仍可抓到最后的任务指标。
	w.shutdownObservability(ctx)
	return nil
}

// newWorkerObservability 在 WORKER_METRICS_ADDR 非空时构造 worker 指标
// Registry（subsystem=worker）与可观测 http.Server；为空时两者都返回 nil。
// 配了 DB 时把连接池 collector 注册进同一份 /metrics。/health 的 Redis 探测
// 走 reg.Queue（asynq 实际使用的 Redis 连接），DB 探测只在配置时参与。
func newWorkerObservability(reg *bootstrap.Registry) (*metrics.Registry, *http.Server) {
	addr := strings.TrimSpace(reg.Cfg.Worker.MetricsAddr)
	if addr == "" {
		return nil, nil
	}
	metricsReg := metrics.New("worker")
	var checks worker.HealthChecks
	// 避免 typed-nil 把接口包成 non-nil。
	if reg.Queue != nil {
		checks.Redis = reg.Queue
	}
	if reg.DB != nil {
		checks.DB = reg.DB
		if dbCollector := reg.DB.Collector(); dbCollector != nil {
			metricsReg.MustRegister(dbCollector)
		}
	}
	return metricsReg, worker.NewHTTPServer(addr, metricsReg.Handler(), checks)
}

// listenObservability 同步绑定可观测端口；未启用时返回 nil listener。
func (w *Worker) listenObservability() (net.Listener, error) {
	if w.http == nil {
		return nil, nil
	}
	ln, err := net.Listen("tcp", w.http.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen worker observability on %s: %w", w.http.Addr, err)
	}
	return ln, nil
}

// serveObservability 在后台 Serve 已绑定的 listener。运行期异常只记 error
// 日志，不打断任务消费（可观测端口不是业务关键路径）。
func (w *Worker) serveObservability(ln net.Listener) {
	if w.http == nil || ln == nil {
		return
	}
	go func() {
		if err := w.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			applog.L().Error("worker observability server error", zap.Error(err))
		}
	}()
}

// shutdownObservability 带超时优雅关闭可观测端口。ctx 此时通常已取消，用
// WithoutCancel 继承其 value 但摆脱取消信号，再单独套超时。
func (w *Worker) shutdownObservability(ctx context.Context) {
	if w.http == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observabilityShutdownTimeout)
	defer cancel()
	if err := w.http.Shutdown(shutdownCtx); err != nil {
		applog.L().Warn("worker observability shutdown failed", zap.Error(err))
	}
}

// validateWorkerRegistry 校验 Worker 装配需要的 Registry 字段都齐了。
// Worker 必需 Redis；DB 是可选的（不依赖 DB 的任务也是合法的）。
func validateWorkerRegistry(reg *bootstrap.Registry) error {
	switch {
	case reg == nil:
		return errNilRegistry
	case reg.Cfg == nil:
		return errNilConfig
	case reg.Cfg.Redis.Addr == "":
		return fmt.Errorf("app: missing redis address")
	default:
		return nil
	}
}

// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。
//
// Example processor 走 typed contract：reg.DB 可用时注入真 ExampleService
// （走 repository → sqlc 落库），DB 不可用时让 RegisterHandlers 回填
// noopExampleProcessor 兜底，便于无 DB 的 worker 部署形态（如只跑外部 API
// 任务）也能起得来。worker 包本身不 import pgx / sqlcdb，符合分层规则。
//
// 安全门槛：APP_ENV=production 下调 deps.RequiredProcessors() 显式检查
// 每个 task processor 是否真注入。任一 missing 就 fail-fast——production
// 漏注入意味着任务会被 noop 消费 + ack 掉，比 panic 更危险（消息消失但
// 只打 warn 日志）。dev / staging 仍然允许 noop，方便从模板态启动。
//
// 新增 task 类型时**不必改本函数**：在对应 service / repository 装配处加
// `deps.Xxx = service.NewXxxService(...)` 即可，production guard 通过
// `deps.RequiredProcessors()` 自动扩展（前提是新 processor 已加进
// worker.Deps 的 RequiredProcessors 返回列表）。
func buildWorkerDeps(reg *bootstrap.Registry) (*worker.Deps, error) {
	deps := &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}
	if reg.DB != nil {
		repo := repository.NewExampleRepository(reg.DB.Pool())
		deps.Example = service.NewExampleService(repo, reg.Queue)
	}
	if reg.Cfg != nil && reg.Cfg.Env.IsProduction() {
		var missing []string
		for _, req := range deps.RequiredProcessors() {
			if !req.Present {
				missing = append(missing, req.Name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("worker: production refusing to start; missing real processors %v (tasks would be silently ack'd by noop fallback)", missing)
		}
	}
	return deps, nil
}
