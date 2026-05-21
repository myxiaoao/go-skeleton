package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
	"go.uber.org/zap"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/migrations"
	"go-skeleton/pkg/buildinfo"
	appdb "go-skeleton/pkg/database"
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
//
// 用 goose 的 Provider API（不是全局函数）：
//   - 绑死 Postgres 方言（本项目只支持 PG），不拉其他方言的注册与依赖；
//   - 配 Postgres advisory lock（session locker），多实例/多机同时跑 migrate 时
//     只有一个能拿锁、其余阻塞等待，把"migrate 仅一次"从靠人工纪律变成机制保证；
//   - 结果以结构化返回（[]*MigrationResult / []*MigrationStatus），用 zap 汇报，
//     不依赖 goose 的全局 stdout logger。
//
// 关于 go.sum 里的 modernc.org/sqlite：那是 goose 主包（github.com/pressly/goose/v3）
// 源码里 import 的，所以留在模块依赖图、go.sum 必须记其校验和，`go mod tidy` 删不掉。
// 但"在依赖图里"≠"进二进制"——本文件只 import Provider + DialectPostgres，sqlite 那套
// package 没被实际 import，Go 的死代码消除会把它剔掉。`go list -deps ./cmd/migrate`
// 里没有任何 sqlite/其他方言驱动可证。看到 go.sum 里的 sqlite 不必折腾去删。
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

	dbMgr, err := appdb.Init(appdb.Config{
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

	// Postgres session-level advisory lock：多实例并发跑 migrate 时串行化，只有
	// 持锁者执行、其余阻塞等待（默认重试 5s × 60 = 最多 5min），杜绝并发 DDL /
	// 版本表竞态。把"migrate 仅一次"从靠人工纪律变成机制保证。
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		applog.L().Fatal("create postgres session locker", zap.Error(err))
	}

	provider, err := goose.NewProvider(
		database.DialectPostgres,
		sqlDB,
		migrations.FS,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		applog.L().Fatal("create goose provider", zap.Error(err))
	}

	// 不给整体操作设激进超时：迁移可能长跑，且锁等待本身已有 5min 上限；进程被卡
	// 死时由 systemd 的 TimeoutStopSec 兜底回收。Ping 用短超时单独探活。
	ctx := context.Background()
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := provider.Ping(pingCtx); err != nil {
		applog.L().Fatal("ping database", zap.Error(err))
	}

	switch *cmd {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			applog.L().Fatal("goose up", zap.Error(err))
		}
		for _, r := range results {
			applog.L().Info("migration applied",
				zap.String("direction", r.Direction),
				zap.Int64("version", r.Source.Version),
				zap.String("file", r.Source.Path),
				zap.Duration("duration", r.Duration))
		}
	case "down":
		result, err := provider.Down(ctx)
		if err != nil {
			applog.L().Fatal("goose down", zap.Error(err))
		}
		applog.L().Info("migration rolled back",
			zap.String("direction", result.Direction),
			zap.Int64("version", result.Source.Version),
			zap.String("file", result.Source.Path),
			zap.Duration("duration", result.Duration))
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			applog.L().Fatal("goose status", zap.Error(err))
		}
		for _, s := range statuses {
			appliedAt := "pending"
			if !s.AppliedAt.IsZero() {
				appliedAt = s.AppliedAt.Format(time.RFC3339)
			}
			applog.L().Info("migration status",
				zap.Int64("version", s.Source.Version),
				zap.String("file", s.Source.Path),
				zap.String("state", string(s.State)),
				zap.String("applied_at", appliedAt))
		}
		return
	default:
		applog.L().Fatal("unknown migration command", zap.String("cmd", *cmd))
	}

	if v, err := provider.GetDBVersion(ctx); err == nil {
		applog.L().Info("migrations completed", zap.String("cmd", *cmd), zap.Int64("db_version", v))
	} else {
		applog.L().Info("migrations completed", zap.String("cmd", *cmd))
	}
}
