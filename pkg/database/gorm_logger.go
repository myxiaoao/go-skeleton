package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/utils"

	applog "go-skeleton/pkg/log"
)

// slowQueryThreshold 与 GORM 默认 logger 保持一致。
const slowQueryThreshold = 200 * time.Millisecond

// zapGormLogger 把 GORM 日志转交给 zap（applog.FromContext）。GORM 内置默认
// logger 直接往 stdout 写带 ANSI 颜色的纯文本，会破坏生产 LOG_FORMAT=json
// 的约定，也带不上 trace_id。
type zapGormLogger struct {
	level logger.LogLevel
}

// newGormLogger 按字符串 level 构造 GORM logger。
func newGormLogger(level string) logger.Interface {
	return &zapGormLogger{level: parseLogLevel(level)}
}

func (l *zapGormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &zapGormLogger{level: level}
}

func (l *zapGormLogger) Info(ctx context.Context, msg string, data ...any) {
	l.printf(ctx, logger.Info, zapcore.InfoLevel, msg, data...)
}

func (l *zapGormLogger) Warn(ctx context.Context, msg string, data ...any) {
	l.printf(ctx, logger.Warn, zapcore.WarnLevel, msg, data...)
}

func (l *zapGormLogger) Error(ctx context.Context, msg string, data ...any) {
	l.printf(ctx, logger.Error, zapcore.ErrorLevel, msg, data...)
}

// Trace 在每条 SQL 执行后被调用：出错记 error、超过慢查询阈值记 warn、
// Info 级别下记全部 SQL。ErrRecordNotFound 是正常业务分支，不当错误记。
func (l *zapGormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= logger.Error:
		logQuery(ctx, zapcore.ErrorLevel, "gorm query failed", elapsed, fc, zap.Error(err))
	case elapsed > slowQueryThreshold && l.level >= logger.Warn:
		logQuery(ctx, zapcore.WarnLevel, "gorm slow query", elapsed, fc, zap.Duration("threshold", slowQueryThreshold))
	case l.level >= logger.Info:
		logQuery(ctx, zapcore.InfoLevel, "gorm query", elapsed, fc)
	}
}

func (l *zapGormLogger) printf(ctx context.Context, minLevel logger.LogLevel, level zapcore.Level, msg string, data ...any) {
	if l.level >= minLevel {
		applog.FromContext(ctx).Log(level, fmt.Sprintf(msg, data...), zap.String("source", utils.FileWithLineNum()))
	}
}

// logQuery 只在确定要输出时才调 fc()——它会把 SQL 变量插值成完整语句，有开销。
func logQuery(ctx context.Context, level zapcore.Level, msg string, elapsed time.Duration, fc func() (string, int64), extra ...zap.Field) {
	sql, rows := fc()
	fields := append([]zap.Field{
		zap.String("sql", sql),
		zap.Int64("rows", rows),
		zap.Duration("elapsed", elapsed),
		zap.String("source", utils.FileWithLineNum()),
	}, extra...)
	applog.FromContext(ctx).Log(level, msg, fields...)
}
