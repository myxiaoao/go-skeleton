package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
	"go.uber.org/zap"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/migrations"
	applog "go-skeleton/pkg/log"
)

// runMigrate 是 migrate 子命令：用 goose 跑版本化 SQL 迁移（migrations/*.sql，已 embed 进二进制）。
//
//	go-skeleton migrate         # up：应用全部待执行迁移（默认）
//	go-skeleton migrate up      # 同上
//	go-skeleton migrate down    # 回滚最近一版
//	go-skeleton migrate status  # 打印各迁移的应用状态
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
// package 没被实际 import，Go 的死代码消除会把它剔掉。`go list -deps ./cmd/go-skeleton`
// 里没有任何 sqlite/其他方言驱动可证。看到 go.sum 里的 sqlite 不必折腾去删。
func runMigrate(args []string) error {
	cmd, err := parseMigrateArgs(args)
	if err != nil {
		return err
	}

	cfg, err := bootstrap.LoadConfig("migrate")
	if err != nil {
		return err
	}
	defer func() { _ = applog.Sync() }()

	if err := migrate(cfg, cmd); err != nil {
		applog.L().Fatal("migrate failed", zap.String("cmd", cmd), zap.Error(err))
	}
	return nil
}

// migrate 执行一次 goose 迁移命令（up / down / status），供 migrate 与 all 子命令复用。
// 自己开一份 DB 连接、用完即关，不占用业务进程的连接池。
func migrate(cfg *config.Config, cmd string) error {
	dbMgr, err := bootstrap.InitDatabase(cfg)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer func() {
		if err := dbMgr.Close(); err != nil {
			applog.L().Warn("close database", zap.Error(err))
		}
	}()
	sqlDB := dbMgr.SQLDB()
	if sqlDB == nil {
		return errors.New("database is not configured")
	}
	// sqlDB 是共享 pgx pool 之上的 database/sql 视图，goose 复用它，
	// 不另开自己的连接。

	// Postgres session-level advisory lock：多实例并发跑 migrate 时串行化，只有
	// 持锁者执行、其余阻塞等待（默认重试 5s × 60 = 最多 5min），杜绝并发 DDL /
	// 版本表竞态。把"migrate 仅一次"从靠人工纪律变成机制保证。
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create postgres session locker: %w", err)
	}

	provider, err := goose.NewProvider(
		database.DialectPostgres,
		sqlDB,
		migrations.FS,
		goose.WithSessionLocker(locker),
		// 本项目只认 migrations/*.sql 为真相源。关掉全局 Go migration registry，
		// 杜绝某个包 init 注册的 Go migration 被意外合并进来。
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	// 不给整体操作设激进超时：迁移可能长跑，且锁等待本身已有 5min 上限；进程被卡
	// 死时由 systemd 的 TimeoutStopSec 兜底回收。Ping 用短超时单独探活。
	ctx := context.Background()
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := provider.Ping(pingCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	switch cmd {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("goose up: %w", err)
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
			return fmt.Errorf("goose down: %w", err)
		}
		applog.L().Info("migration rolled back",
			zap.String("direction", result.Direction),
			zap.Int64("version", result.Source.Version),
			zap.String("file", result.Source.Path),
			zap.Duration("duration", result.Duration))
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("goose status: %w", err)
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
		return nil
	}

	if v, err := provider.GetDBVersion(ctx); err == nil {
		applog.L().Info("migrations completed", zap.String("cmd", cmd), zap.Int64("db_version", v))
	} else {
		applog.L().Info("migrations completed", zap.String("cmd", cmd))
	}
	return nil
}

// parseMigrateArgs 解析 migrate 子命令参数：最多一个位置参数，缺省为 up。
// 在加载配置、连库之前校验，拼错命令不会白连一次数据库。
func parseMigrateArgs(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("migrate: too many arguments %v", args)
	}
	cmd := "up"
	if len(args) == 1 {
		cmd = args[0]
	}
	if !slices.Contains([]string{"up", "down", "status"}, cmd) {
		return "", fmt.Errorf("migrate: unknown command %q (want up | down | status)", cmd)
	}
	return cmd, nil
}
