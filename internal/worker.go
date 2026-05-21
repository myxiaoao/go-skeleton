package app

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"

	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/worker"
)

// Worker 持有从 Registry 装配出来的 Asynq 异步任务运行时（server + ServeMux）。
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
}

// NewWorker 装配异步任务 handler 和 worker 运行时。reg 不全时返 error；
// 失败不会启动 server。
func NewWorker(reg *bootstrap.Registry) (*Worker, error) {
	if err := validateWorkerRegistry(reg); err != nil {
		return nil, err
	}

	deps := buildWorkerDeps(reg)
	mux := asynq.NewServeMux()
	worker.RegisterHandlers(mux, deps)

	return &Worker{
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
func (w *Worker) Run(ctx context.Context, onReady func()) error {
	if w == nil || w.server == nil || w.mux == nil {
		return errNilWorker
	}

	if err := w.server.Start(w.mux); err != nil {
		return fmt.Errorf("start worker server: %w", err)
	}
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
	return nil
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

// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。当前骨架
// 的 example handler 不需要 DB，所以只挂 Cache + Queue；真实业务参考代码
// 内示意把 service 注入 Example 字段。
func buildWorkerDeps(reg *bootstrap.Registry) *worker.Deps {
	// 示意：业务任务需要 DB 时，在这里组装 service：
	//
	//   var exampleSvc worker.ExampleProcessor
	//   if reg.DB != nil {
	//       repo := repository.NewExampleRepository(reg.DB.DB())
	//       exampleSvc = service.NewExampleService(repo, reg.Queue)
	//   }
	//
	// 然后把 exampleSvc 挂到 Deps.Example。worker 包本身不 import gorm。
	return &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}
}
