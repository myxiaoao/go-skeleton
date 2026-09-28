package database

import (
	"testing"
	"time"
)

// unreachableDSN 指向一个关闭的端口：pgxpool 惰性建连，所以针对它
// 构造 pool 仍然应该成功。
const unreachableDSN = "postgres://u:p@127.0.0.1:1/db?sslmode=disable"

func TestNormalizePoolSettings(t *testing.T) {
	cases := []struct {
		name string
		in   Config
		want poolSettings
	}{
		{"zero uses defaults", Config{}, poolSettings{maxConns: 30, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
		{"custom values kept", Config{MaxConns: 50, MinConns: 5, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute}, poolSettings{maxConns: 50, minConns: 5, connMaxLifetime: time.Hour, connMaxIdleTime: time.Minute}},
		{"negative falls back", Config{MaxConns: -1, MinConns: -1}, poolSettings{maxConns: 30, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
		{"min above max dropped", Config{MaxConns: 10, MinConns: 11}, poolSettings{maxConns: 10, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizePoolSettings(c.in); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestInitEmptyDSNReturnsEmptyManager(t *testing.T) {
	m, err := Init(t.Context(), Config{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.Pool() != nil || m.SQLDB() != nil {
		t.Fatal("empty DSN should yield a manager without pool")
	}
	if err := m.Ping(t.Context()); err == nil {
		t.Fatal("Ping on empty manager should fail")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestInitRejectsInvalidLogLevel(t *testing.T) {
	if _, err := Init(t.Context(), Config{DSN: unreachableDSN, LogLevel: "verbose"}); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestInitBuildsLazyPool(t *testing.T) {
	m, err := Init(t.Context(), Config{DSN: unreachableDSN, MaxConns: 7})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	cfg := m.Pool().Config()
	if cfg.MaxConns != 7 {
		t.Errorf("MaxConns = %d, want 7", cfg.MaxConns)
	}
	if _, ok := cfg.ConnConfig.Tracer.(*queryTracer); !ok {
		t.Errorf("tracer = %T, want *queryTracer", cfg.ConnConfig.Tracer)
	}
	if m.SQLDB() == nil {
		t.Error("SQLDB should share the pool")
	}
}

func TestNilManagerIsSafe(t *testing.T) {
	var m *DBManager
	if m.Pool() != nil || m.SQLDB() != nil {
		t.Fatal("nil manager accessors should return nil")
	}
	if err := m.Ping(t.Context()); err == nil {
		t.Fatal("Ping on nil manager should fail")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close on nil manager: %v", err)
	}
}
