package database

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	applog "go-skeleton/pkg/log"
)

func TestZapGormLoggerTrace(t *testing.T) {
	fast := time.Now()
	slow := time.Now().Add(-2 * slowQueryThreshold)
	cases := []struct {
		name      string
		level     logger.LogLevel
		begin     time.Time
		err       error
		wantLevel zapcore.Level
		wantMsg   string // empty means nothing should be logged
	}{
		{"error logged", logger.Warn, fast, errors.New("boom"), zapcore.ErrorLevel, "gorm query failed"},
		{"record not found ignored", logger.Warn, fast, gorm.ErrRecordNotFound, 0, ""},
		{"slow query warned", logger.Warn, slow, nil, zapcore.WarnLevel, "gorm slow query"},
		{"fast query skipped at warn", logger.Warn, fast, nil, 0, ""},
		{"all queries at info", logger.Info, fast, nil, zapcore.InfoLevel, "gorm query"},
		{"silent drops errors", logger.Silent, fast, errors.New("boom"), 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			defer applog.SetLogger(zap.New(core))()

			ctx := applog.WithTraceID(t.Context(), "trace-db")
			l := newGormLogger("").LogMode(c.level)
			l.Trace(ctx, c.begin, func() (string, int64) { return "SELECT 1", 1 }, c.err)

			entries := logs.All()
			if c.wantMsg == "" {
				if len(entries) != 0 {
					t.Fatalf("expected no log, got %d: %v", len(entries), entries[0].Message)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("got %d log entries, want 1", len(entries))
			}
			e := entries[0]
			if e.Level != c.wantLevel || e.Message != c.wantMsg {
				t.Errorf("got (%v, %q), want (%v, %q)", e.Level, e.Message, c.wantLevel, c.wantMsg)
			}
			fields := e.ContextMap()
			if fields["trace_id"] != "trace-db" || fields["sql"] != "SELECT 1" {
				t.Errorf("missing trace_id/sql fields: %v", fields)
			}
		})
	}
}

func TestZapGormLoggerPrintfRespectsLevel(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	defer applog.SetLogger(zap.New(core))()

	l := newGormLogger("error")
	l.Warn(t.Context(), "dropped %d", 1)
	l.Error(t.Context(), "kept %d", 2)

	entries := logs.All()
	if len(entries) != 1 || entries[0].Message != "kept 2" {
		t.Fatalf("got %v, want single 'kept 2' entry", entries)
	}
}
