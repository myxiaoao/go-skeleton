package cli

import (
	"context"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"go-skeleton/config"
	app "go-skeleton/internal"
	"go-skeleton/internal/bootstrap"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/sdnotify"
)

// runWorker 启动 Asynq 消费者：加载配置 → 初始化 → 启动 → 信号优雅关闭。
func runWorker(args []string) error {
	if err := noArgs("worker", args); err != nil {
		return err
	}

	cfg, err := bootstrap.LoadConfig("worker")
	if err != nil {
		return err
	}
	defer func() { _ = applog.Sync() }()

	// production 下输出 worker 专属的"非致命但大概率漏配"提示（如可观测端口
	// 监听全部网卡）。硬拦项已经在 config.validate 阶段 fail-fast 拦掉。
	for _, w := range config.WorkerProductionWarnings(cfg) {
		applog.L().Warn("production config warning", zap.String("hint", w))
	}

	registry, err := bootstrap.InitWorker(cfg)
	if err != nil {
		applog.L().Fatal("initialize worker", zap.Error(err))
	}
	defer func() {
		if err := registry.Close(); err != nil {
			applog.L().Warn("close registry", zap.Error(err))
		}
	}()

	worker, err := app.NewWorker(registry)
	if err != nil {
		applog.L().Fatal("assemble worker", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// sd_notify watchdog 心跳：systemd Type=notify + WatchdogSec 时，定期发
	// WATCHDOG=1 让 systemd 知道进程还活着。注意心跳是独立 goroutine 按固定
	// 间隔发送，只能兜住"整个进程卡死 / 调度不动"这类故障；单个 Asynq handler
	// 挂死**不会**让心跳停止，这类问题靠 task Timeout（task.DefaultOptions）
	// 与 retry / archived 指标告警发现。ctx 取消时自然退出；非 Linux 平台是 noop stub。
	// READY=1 不在这里发——推迟到 worker 进入消费态（worker.Run 的 onReady）。
	go sdnotify.Watchdog(ctx, cfg.Server.WatchdogInterval)

	applog.L().Info("worker started", zap.String("component", "asynq"))
	if err := worker.Run(ctx, sdnotify.Ready); err != nil {
		applog.L().Fatal("worker runtime error", zap.Error(err))
	}
	applog.L().Info("worker shutdown completed")
	return nil
}
