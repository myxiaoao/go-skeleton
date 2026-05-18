package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/model"
	"go-skeleton/pkg/buildinfo"
	"go-skeleton/pkg/database"
	applog "go-skeleton/pkg/log"
)

func main() {
	showVersion := flag.Bool("version", false, "print version info and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.String())
		os.Exit(0)
	}

	config.LoadEnv("cmd/migrate/.env")
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}
	if err := bootstrap.InitRuntime(cfg, "migrate"); err != nil {
		panic(fmt.Sprintf("init runtime: %v", err))
	}
	defer func() { _ = applog.Sync() }()

	dbMgr, err := database.Init(database.Config{
		DSN:             cfg.Postgres.DSN,
		LogLevel:        cfg.Postgres.LogLevel,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,
	})
	if err != nil {
		applog.L().Fatal("initialize database", zap.Error(err))
	}
	defer func() {
		if err := dbMgr.Close(); err != nil {
			applog.L().Warn("close database", zap.Error(err))
		}
	}()
	if dbMgr.DB() == nil {
		applog.L().Fatal("database is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := dbMgr.DB().WithContext(ctx).AutoMigrate(&model.Example{}); err != nil {
		applog.L().Fatal("run migrations", zap.Error(err))
	}
	applog.L().Info("migrations completed")
}
