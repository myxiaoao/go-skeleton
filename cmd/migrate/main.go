package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pressly/goose/v3"
	"go.uber.org/zap"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/migrations"
	"go-skeleton/pkg/buildinfo"
	"go-skeleton/pkg/database"
	applog "go-skeleton/pkg/log"
)

// migrate 进程：用 goose 跑版本化 SQL 迁移（migrations/*.sql，已 embed 进二进制）。
//
//	go run ./cmd/migrate            # up：应用全部待执行迁移（默认）
//	go run ./cmd/migrate -cmd up    # 同上
//	go run ./cmd/migrate -cmd down  # 回滚最近一版
//	go run ./cmd/migrate -cmd status# 打印各迁移的应用状态
//
// 真相源是 migrations/ 下的 SQL 文件，不是 Go struct——AutoMigrate 已移除。
func main() {
	showVersion := flag.Bool("version", false, "print version info and exit")
	cmd := flag.String("cmd", "up", "migration command: up | down | status")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.String())
		os.Exit(0)
	}

	config.LoadEnv("cmd/migrate/.env")
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := bootstrap.InitRuntime(cfg, "migrate"); err != nil {
		fmt.Fprintf(os.Stderr, "init runtime: %v\n", err)
		os.Exit(1)
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

	// 从 *gorm.DB 取底层 *sql.DB 喂给 goose，复用现有连接池，不另开连接。
	sqlDB, err := dbMgr.DB().DB()
	if err != nil {
		applog.L().Fatal("get sql.DB from gorm", zap.Error(err))
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		applog.L().Fatal("set goose dialect", zap.Error(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch *cmd {
	case "up":
		// up/down 静音 goose 自带 log，迁移结果由下方 zap 结构化日志统一汇报。
		goose.SetLogger(goose.NopLogger())
		if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
			applog.L().Fatal("goose up", zap.Error(err))
		}
	case "down":
		goose.SetLogger(goose.NopLogger())
		if err := goose.DownContext(ctx, sqlDB, "."); err != nil {
			applog.L().Fatal("goose down", zap.Error(err))
		}
	case "status":
		// status 的价值就是给人看每条迁移的应用明细表格，保留 goose 默认 stdout
		// 输出，不静音、也不再追加 zap 汇报。
		if err := goose.StatusContext(ctx, sqlDB, "."); err != nil {
			applog.L().Fatal("goose status", zap.Error(err))
		}
		return
	default:
		applog.L().Fatal("unknown migration command", zap.String("cmd", *cmd))
	}

	if v, err := goose.GetDBVersionContext(ctx, sqlDB); err == nil {
		applog.L().Info("migrations completed", zap.String("cmd", *cmd), zap.Int64("db_version", v))
	} else {
		applog.L().Info("migrations completed", zap.String("cmd", *cmd))
	}
}
