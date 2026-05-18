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
		panic(fmt.Sprintf("load config: %v", err))
	}
	if err := bootstrap.InitRuntime(cfg, "worker"); err != nil {
		panic(fmt.Sprintf("init runtime: %v", err))
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

	applog.L().Info("worker started", zap.String("component", "asynq"))
	if err := worker.Run(ctx); err != nil {
		applog.L().Fatal("worker runtime error", zap.Error(err))
	}
	applog.L().Info("worker shutdown completed")
}
