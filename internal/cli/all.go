package cli

import (
	"context"
	"errors"
	"os/signal"
	"sync/atomic"
	"syscall"

	"go.uber.org/zap"

	"go-skeleton/config"
	app "go-skeleton/internal"
	"go-skeleton/internal/bootstrap"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/sdnotify"
)

// runAll 是不带子命令时的默认模式：单进程内先跑 migrate up，再同时启动 API 与 Worker。
// 适合小规模单机部署；需要 API / Worker 分开扩缩容时改用 api / worker 子命令。
//
// 生命周期：
//   - API 与 Worker 共用同一份 Registry（DB / Redis 连接池只建一份）；
//   - 两者都就绪后才发 READY=1；
//   - 任一方启动失败或运行期出错，另一方随之停服，进程以非零码退出；
//   - SIGTERM 时 API drain + Shutdown 与 Worker 两阶段停服并行进行，缩短总停服时长；
//     drain 窗口内 API 投递的任务留在 Redis，下次启动继续消费，不会丢。
func runAll(args []string) error {
	if err := noArgs("all", args); err != nil {
		return err
	}

	cfg, err := bootstrap.LoadConfig("all")
	if err != nil {
		return err
	}
	defer func() { _ = applog.Sync() }()

	for _, w := range append(config.ProductionWarnings(cfg), config.WorkerProductionWarnings(cfg)...) {
		applog.L().Warn("production config warning", zap.String("hint", w))
	}

	// 迁移先于业务进程完成；多实例同时启动时由 advisory lock 串行化。
	if err := migrate(cfg, "up"); err != nil {
		applog.L().Fatal("migrate failed", zap.Error(err))
	}

	// InitAPI 的 Registry 是 Worker 所需依赖的超集（Worker 额外要求 Redis，
	// 未配置时 NewWorker 会报错）。
	registry, err := bootstrap.InitAPI(cfg)
	if err != nil {
		applog.L().Fatal("initialize application", zap.Error(err))
	}
	defer func() {
		if err := registry.Close(); err != nil {
			applog.L().Warn("close registry", zap.Error(err))
		}
	}()

	server, err := app.NewServer(registry)
	if err != nil {
		applog.L().Fatal("assemble http server", zap.Error(err))
	}
	worker, err := app.NewWorker(registry)
	if err != nil {
		applog.L().Fatal("assemble worker", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go sdnotify.Watchdog(ctx, cfg.Server.WatchdogInterval)

	// runCtx 额外承担"一方退出、另一方跟着停"的联动。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := readyAfter(2, sdnotify.Ready)

	workerErr := make(chan error, 1)
	go func() {
		applog.L().Info("worker started", zap.String("component", "asynq"))
		workerErr <- worker.Run(runCtx, ready)
		cancel()
	}()

	apiErr := serveAPI(runCtx, registry, server, ready)
	cancel()
	if err := errors.Join(apiErr, <-workerErr); err != nil {
		applog.L().Fatal("runtime error", zap.Error(err))
	}
	applog.L().Info("all services shutdown completed")
	return nil
}

// readyAfter 返回一个回调：被调用 n 次后才执行一次 fn，用于等多个组件都就绪再发 READY。
func readyAfter(n int32, fn func()) func() {
	var remaining atomic.Int32
	remaining.Store(n)
	return func() {
		if remaining.Add(-1) == 0 {
			fn()
		}
	}
}
