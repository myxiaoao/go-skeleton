package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	applog "go-skeleton/pkg/log"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    logLevel
		wantErr bool
	}{
		{"silent", logSilent, false},
		{"error", logError, false},
		{"", logWarn, false},
		{"WARN", logWarn, false},
		{" info ", logInfo, false},
		{"verbose", logWarn, true},
	}
	for _, c := range cases {
		got, err := parseLogLevel(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("parseLogLevel(%q) = (%v, %v), want (%v, err=%v)", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestQueryTracerStart(t *testing.T) {
	data := pgx.TraceQueryStartData{SQL: "SELECT 1"}

	silent := &queryTracer{level: logSilent}
	if ctx := silent.TraceQueryStart(t.Context(), nil, data); ctx.Value(traceKey{}) != nil {
		t.Fatal("silent tracer should not attach trace data")
	}

	warn := &queryTracer{level: logWarn}
	td, ok := warn.TraceQueryStart(t.Context(), nil, data).Value(traceKey{}).(traceData)
	if !ok || td.sql != "SELECT 1" || td.start.IsZero() {
		t.Fatalf("trace data = %+v, ok=%v", td, ok)
	}
}

func TestQueryTracerEnd(t *testing.T) {
	fast := time.Now()
	slow := time.Now().Add(-2 * slowQueryThreshold)
	boom := errors.New("boom")
	canceled := errors.Join(errors.New("query"), context.Canceled)
	cases := []struct {
		name      string
		level     logLevel
		start     time.Time
		err       error
		wantLevel zapcore.Level
		wantMsg   string // empty means nothing should be logged
	}{
		{"error logged at warn", logWarn, fast, boom, zapcore.ErrorLevel, "db query failed"},
		{"error logged at error", logError, fast, boom, zapcore.ErrorLevel, "db query failed"},
		{"slow error still error", logWarn, slow, boom, zapcore.ErrorLevel, "db query failed"},
		{"canceled downgraded to warn", logWarn, fast, canceled, zapcore.WarnLevel, "db query canceled"},
		{"canceled hidden at error", logError, fast, canceled, 0, ""},
		{"slow query warned", logWarn, slow, nil, zapcore.WarnLevel, "db slow query"},
		{"fast query skipped at warn", logWarn, fast, nil, 0, ""},
		{"all queries at info", logInfo, fast, nil, zapcore.InfoLevel, "db query"},
		{"silent drops errors", logSilent, fast, boom, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			defer applog.SetLogger(zap.New(core))()

			ctx := applog.WithTraceID(t.Context(), "trace-db")
			ctx = context.WithValue(ctx, traceKey{}, traceData{start: c.start, sql: "SELECT $1"})
			tr := &queryTracer{level: c.level}
			tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1"), Err: c.err})

			entries := logs.All()
			if c.wantMsg == "" {
				if len(entries) != 0 {
					t.Fatalf("expected no log, got %q", entries[0].Message)
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
			if fields["trace_id"] != "trace-db" || fields["sql"] != "SELECT $1" || fields["rows"] != int64(1) {
				t.Errorf("unexpected fields: %v", fields)
			}
		})
	}
}

func TestQueryTracerEndWithoutStartIsNoop(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	defer applog.SetLogger(zap.New(core))()

	(&queryTracer{level: logInfo}).TraceQueryEnd(t.Context(), nil, pgx.TraceQueryEndData{})
	if logs.Len() != 0 {
		t.Fatalf("expected no log without start data, got %d", logs.Len())
	}
}
