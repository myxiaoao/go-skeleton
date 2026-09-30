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

// slowQueryThreshold 是慢查询阈值，超过它的查询按 warn 级别记录。
const slowQueryThreshold = 200 * time.Millisecond

// logLevel 对应 DB_LOG_LEVEL；数值越大记录得越多。
type logLevel int

const (
	logSilent logLevel = iota
	logError
	logWarn
	logInfo
)

// parseLogLevel 把 DB_LOG_LEVEL 映射成 logLevel。空值等同 warn。未知
// 取值这里和 config.validate 都会拒绝，让绕开 config 直接调用的
// caller 也能明确报错，而不是静默降级。
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

// queryTracer 实现 pgx.QueryTracer，把 SQL 日志经 applog.FromContext
// 输出，让日志带上 trace_id。只记录 SQL 模板本身、绝不记录参数值，
// 避免敏感数据落进日志。
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
		// 客户端主动断开，不算数据库故障。
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db query canceled", zap.Error(data.Err)
	case errors.Is(data.Err, context.DeadlineExceeded):
		// 请求超时由 timeout 中间件 / 上游 deadline 触发，属预期内的取消，不算数据库故障。
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db query timed out", zap.Error(data.Err)
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
