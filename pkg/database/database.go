package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	applog "go-skeleton/pkg/log"
)

var errNotConfigured = errors.New("database is not configured")

// DBManager 持有 Postgres 连接池。pool 是唯一真正的连接池；
// sqlDB 是同一个 pool 之上的 database/sql 视图，给需要 *sql.DB
// 的库（如 goose）用。
type DBManager struct {
	pool  *pgxpool.Pool
	sqlDB *sql.DB
}

// Config 是数据库连接配置：DSN + 连接池参数。
type Config struct {
	DSN             string
	LogLevel        string
	MaxConns        int
	MinConns        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// poolSettings 是连接池参数补完默认值之后的 Config。
type poolSettings struct {
	maxConns        int32
	minConns        int32
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

// Init 建连接池。DSN 为空时返回空 manager（不报错），让
// InitAPI / InitWorker 决定数据库是否必需。
//
// pgxpool 惰性建连，Init 本身不 ping：启动期 fail-fast 由
// bootstrap.probeDependencies（API / Worker）和 cmd/migrate 里
// 显式的 ping 各自覆盖。
func Init(ctx context.Context, cfg Config) (*DBManager, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return &DBManager{}, nil
	}
	poolCfg, err := newPoolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	applog.L().Info("postgres pool created")
	return NewManager(pool), nil
}

// NewManager 包装一个已有的 pool。测试可以传一个指向不可达地址的 pool，
// 因为 pgxpool 在首次使用前不会真正建连。
func NewManager(pool *pgxpool.Pool) *DBManager {
	return &DBManager{pool: pool, sqlDB: stdlib.OpenDBFromPool(pool)}
}

// Pool 返回 pgx pool。只有 repository 装配层应该用它。
func (m *DBManager) Pool() *pgxpool.Pool {
	if m == nil {
		return nil
	}
	return m.pool
}

// SQLDB 返回同一个 pool 之上的 database/sql 句柄。
func (m *DBManager) SQLDB() *sql.DB {
	if m == nil {
		return nil
	}
	return m.sqlDB
}

// Ping 探测数据库是否可达；/health 会带短超时 ctx 调它。
func (m *DBManager) Ping(ctx context.Context) error {
	if m == nil || m.pool == nil {
		return errNotConfigured
	}
	if err := m.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close 先关 sqlDB（它不拥有 pool），再关 pool。
// nil-safe，bootstrap.Registry.Close 会调它。
func (m *DBManager) Close() error {
	if m == nil || m.pool == nil {
		return nil
	}
	err := m.sqlDB.Close()
	m.pool.Close()
	return err
}

func newPoolConfig(cfg Config) (*pgxpool.Config, error) {
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	s := normalizePoolSettings(cfg)
	poolCfg.MaxConns = s.maxConns
	poolCfg.MinConns = s.minConns
	poolCfg.MaxConnLifetime = s.connMaxLifetime
	poolCfg.MaxConnIdleTime = s.connMaxIdleTime
	poolCfg.ConnConfig.Tracer = &queryTracer{level: level}
	return poolCfg, nil
}

// normalizePoolSettings 给零值 / 越界值补上默认值，这样 caller
// 不必填满每个字段。
func normalizePoolSettings(cfg Config) poolSettings {
	s := poolSettings{
		maxConns:        30,
		connMaxLifetime: 30 * time.Minute,
		connMaxIdleTime: 5 * time.Minute,
	}
	if cfg.MaxConns > 0 && cfg.MaxConns <= math.MaxInt32 {
		s.maxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 && cfg.MinConns <= math.MaxInt32 && int32(cfg.MinConns) <= s.maxConns {
		s.minConns = int32(cfg.MinConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		s.connMaxLifetime = cfg.ConnMaxLifetime
	}
	if cfg.ConnMaxIdleTime > 0 {
		s.connMaxIdleTime = cfg.ConnMaxIdleTime
	}
	return s
}
