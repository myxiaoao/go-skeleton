package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	applog "go-skeleton/pkg/log"
)

// slowQueryThreshold marks queries slower than this as warn.
const slowQueryThreshold = 200 * time.Millisecond

// logLevel mirrors DB_LOG_LEVEL; higher values log more.
type logLevel int

const (
	logSilent logLevel = iota
	logError
	logWarn
	logInfo
)

// parseLogLevel maps DB_LOG_LEVEL to logLevel. Empty means warn. Unknown
// values are rejected here as well as in config.validate, so callers that
// bypass config still fail loudly.
func parseLogLevel(level string) (logLevel, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		return logSilent, nil
	case "error":
		return logError, nil
	case "warn", "":
		return logWarn, nil
	case "info":
		return logInfo, nil
	default:
		return logWarn, fmt.Errorf("unknown db log level %q (want silent/error/warn/info)", level)
	}
}

type traceKey struct{}

type traceData struct {
	start time.Time
	sql   string
}

// queryTracer implements pgx.QueryTracer and routes SQL logs through
// applog.FromContext so they carry trace_id. Only the SQL template is
// logged, never argument values, to keep sensitive data out of logs.
type queryTracer struct {
	level logLevel
}

var _ pgx.QueryTracer = (*queryTracer)(nil)

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.level == logSilent {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, traceData{start: time.Now(), sql: data.SQL})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	td, ok := ctx.Value(traceKey{}).(traceData)
	if !ok {
		return
	}
	elapsed := time.Since(td.start)

	var (
		need  logLevel
		level zapcore.Level
		msg   string
		extra zap.Field
	)
	switch {
	case errors.Is(data.Err, context.Canceled):
		// Client went away; not a database fault.
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db query canceled", zap.Error(data.Err)
	case data.Err != nil:
		need, level, msg, extra = logError, zapcore.ErrorLevel, "db query failed", zap.Error(data.Err)
	case elapsed > slowQueryThreshold:
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db slow query", zap.Duration("threshold", slowQueryThreshold)
	default:
		need, level, msg, extra = logInfo, zapcore.InfoLevel, "db query", zap.Skip()
	}
	if t.level < need {
		return
	}
	applog.FromContext(ctx).Log(level, msg,
		zap.String("sql", td.sql),
		zap.Int64("rows", data.CommandTag.RowsAffected()),
		zap.Duration("elapsed", elapsed),
		extra,
	)
}
