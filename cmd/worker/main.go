package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"go-skeleton/config"
	app "go-skeleton/internal"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/pkg/buildinfo"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/sdnotify"
)

func main() {
	showVersion := flag.Bool("version", false, "print version info and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.String())
		os.Exit(0)
	}

	config.LoadEnv("cmd/worker/.env")
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := bootstrap.InitRuntime(cfg, "worker"); err != nil {
		fmt.Fprintf(os.Stderr, "init runtime: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = applog.Sync() }()

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
	// WATCHDOG=1 让 systemd 知道 worker 还活着；如果 Asynq handler 卡死（不
	// panic、不 ErrorHandler），systemd 会按 unit 的 Restart=on-failure 重启。
	// ctx 取消时自然退出；非 Linux 平台是 noop stub。
	// READY=1 不在这里发——推迟到 worker 进入消费态（worker.Run 的 onReady）。
	go sdnotify.Watchdog(ctx, cfg.Server.WatchdogInterval)

	applog.L().Info("worker started", zap.String("component", "asynq"))
	if err := worker.Run(ctx, sdnotify.Ready); err != nil {
		applog.L().Fatal("worker runtime error", zap.Error(err))
	}
	applog.L().Info("worker shutdown completed")
}
