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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	applog "go-skeleton/pkg/log"
)

var errNotConfigured = errors.New("database is not configured")

// DBManager owns the Postgres connection pool. pool is the only real pool;
// sqlDB is a database/sql view over the same pool for libraries that need
// *sql.DB (goose).
type DBManager struct {
	pool  *pgxpool.Pool
	sqlDB *sql.DB
	// gorm is a temporary bridge sharing sqlDB until repositories move to
	// sqlc; it is removed afterwards.
	gorm *gorm.DB
}

// Config is the database connection config: DSN + pool settings.
type Config struct {
	DSN             string
	LogLevel        string
	MaxConns        int
	MinConns        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// poolSettings is Config after defaults are applied.
type poolSettings struct {
	maxConns        int32
	minConns        int32
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

// Init builds the pool. An empty DSN yields an empty manager (no error) so
// InitAPI / InitWorker decide whether the database is required.
//
// pgxpool connects lazily and Init does not ping: startup fail-fast is
// covered by bootstrap.probeDependencies (API / Worker) and the explicit
// ping in cmd/migrate.
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
	m, err := NewManager(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	applog.L().Info("postgres pool created")
	return m, nil
}

// NewManager wraps an existing pool. Tests can pass a pool pointing at an
// unreachable address because pgxpool does not connect until first use.
func NewManager(pool *pgxpool.Pool) (*DBManager, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:               logger.Discard, // SQL is logged by queryTracer
		DisableAutomaticPing: true,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open gorm on shared pool: %w", err)
	}
	return &DBManager{pool: pool, sqlDB: sqlDB, gorm: gdb}, nil
}

// Pool returns the pgx pool. Only repository wiring should use it.
func (m *DBManager) Pool() *pgxpool.Pool {
	if m == nil {
		return nil
	}
	return m.pool
}

// SQLDB returns a database/sql handle backed by the same pool.
func (m *DBManager) SQLDB() *sql.DB {
	if m == nil {
		return nil
	}
	return m.sqlDB
}

// DB returns the temporary GORM bridge.
func (m *DBManager) DB() *gorm.DB {
	if m == nil {
		return nil
	}
	return m.gorm
}

// Ping checks reachability; /health calls it with a short-timeout ctx.
func (m *DBManager) Ping(ctx context.Context) error {
	if m == nil || m.pool == nil {
		return errNotConfigured
	}
	if err := m.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close closes sqlDB first (it does not own the pool), then the pool.
// nil-safe; bootstrap.Registry.Close calls it.
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

// normalizePoolSettings applies defaults for zero / out-of-range values so
// callers need not fill every field.
func normalizePoolSettings(cfg Config) poolSettings {
	s := poolSettings{
		maxConns:        30,
		connMaxLifetime: 30 * time.Minute,
		connMaxIdleTime: 5 * time.Minute,
	}
	if cfg.MaxConns > 0 && cfg.MaxConns <= math.MaxInt32 {
		s.maxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 && cfg.MinConns <= int(s.maxConns) {
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
