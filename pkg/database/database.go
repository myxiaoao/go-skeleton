package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	applog "go-skeleton/pkg/log"
)

// DBManager 持有主数据库连接（*gorm.DB）。预留 primary 字段名是为了将来要
// 加只读副本时不破坏 API。
type DBManager struct {
	primary *gorm.DB
}

// Config 是数据库连接配置：DSN + 连接池参数。
type Config struct {
	DSN             string
	LogLevel        string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// poolSettings 是连接池经过 normalize（兜底默认值）后的最终配置。
type poolSettings struct {
	maxIdleConns    int
	maxOpenConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

// Init 打开主数据库连接。
//
// DSN 为空时返回**带 nil DB 的空 manager**（不报错），让上层 InitAPI /
// InitWorker 决定 DB 是必需还是可选（Worker 进程可以没 DB）。
func Init(cfg Config) (*DBManager, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return &DBManager{}, nil
	}

	pool := normalizePoolSettings(cfg)
	primary, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger:                                   newGormLogger(cfg.LogLevel),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := tunePool(primary, pool); err != nil {
		return nil, err
	}

	applog.L().Info("postgres connected")
	return &DBManager{primary: primary}, nil
}

// DB 返回底层 *gorm.DB。仅 repository 层应该调它；其他层通过 service 包里
// 的接口隔离，不直接拿 *gorm.DB（见 CLAUDE.md 分层规则）。
func (m *DBManager) DB() *gorm.DB {
	if m == nil {
		return nil
	}
	return m.primary
}

// Ping 探测数据库是否可达。/health 探针会调它，所以 ctx 应该带短超时。
func (m *DBManager) Ping(ctx context.Context) error {
	if m == nil || m.primary == nil {
		return fmt.Errorf("database is not configured")
	}
	sqlDB, err := m.primary.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close 关闭连接池。nil-safe，bootstrap.Registry.Close 会调它。
func (m *DBManager) Close() error {
	if m == nil || m.primary == nil {
		return nil
	}
	sqlDB, err := m.primary.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// NewTestManager 从已有的 *gorm.DB（例如 DryRun 模式）构造 manager，给测试用。
func NewTestManager(db *gorm.DB) *DBManager {
	return &DBManager{primary: db}
}

// tunePool 把连接池参数挂到底层 sql.DB 上。GORM 不直接暴露这些 setter，
// 必须取出 sql.DB 后设置。
func tunePool(db *gorm.DB, pool poolSettings) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxIdleConns(pool.maxIdleConns)
	sqlDB.SetMaxOpenConns(pool.maxOpenConns)
	sqlDB.SetConnMaxLifetime(pool.connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(pool.connMaxIdleTime)
	return nil
}

// normalizePoolSettings 给连接池参数补兜底默认值。零 / 负值替换成合理默认，
// 这样 Config 的字段可以不强制要求 caller 都填。
func normalizePoolSettings(cfg Config) poolSettings {
	settings := poolSettings{
		maxIdleConns:    cfg.MaxIdleConns,
		maxOpenConns:    cfg.MaxOpenConns,
		connMaxLifetime: cfg.ConnMaxLifetime,
		connMaxIdleTime: cfg.ConnMaxIdleTime,
	}
	if settings.maxIdleConns <= 0 {
		settings.maxIdleConns = 15
	}
	if settings.maxOpenConns <= 0 {
		settings.maxOpenConns = 30
	}
	if settings.connMaxLifetime <= 0 {
		settings.connMaxLifetime = 30 * time.Minute
	}
	if settings.connMaxIdleTime <= 0 {
		settings.connMaxIdleTime = 5 * time.Minute
	}
	return settings
}

// parseLogLevel 把字符串 log 级别（环境变量来）翻译成 GORM 的 logger.LogLevel。
// 默认 warn；未知值打告警日志后也回退到 warn，避免误关错误日志。
func parseLogLevel(level string) logger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "info":
		return logger.Info
	case "warn", "warning", "":
		return logger.Warn
	default:
		applog.L().Warn("unknown gorm log level; falling back to warn", zap.String("level", level))
		return logger.Warn
	}
}
