package log

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config 控制 logger 初始化：Level 是最低输出级别、Format 决定编码格式
// （json / console）、StacktraceLevel 是开堆栈的阈值、Service 写到每条日志
// 的 service 字段方便采集端按进程区分。
type Config struct {
	Level           string
	Format          string
	StacktraceLevel string
	Service         string
}

// ctxKey 是 context 里存放 trace_id 的私有键，用空 struct 类型避免和外部
// context value 撞 key。
type ctxKey struct{}

// defaultLogger 用 atomic.Pointer 保护并发读写——Init / SetLogger 可能在测试
// 期间替换全局 logger，避免和业务代码竞争。
var defaultLogger atomic.Pointer[zap.Logger]

func init() {
	defaultLogger.Store(zap.NewNop())
}

// Init 初始化全局 zap logger 并存到 defaultLogger。Format 不识别时报错，
// 让 cmd/*/main.go fail-fast 退出而不是默默回落到 nop。
func Init(cfg Config) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	stacktraceLevel, err := parseLevel(cfg.StacktraceLevel)
	if err != nil {
		return nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	development := cfg.Format == "console"
	if development {
		encoderCfg = zap.NewDevelopmentEncoderConfig()
	}
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	switch cfg.Format {
	case "", "json":
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Format)
	}

	options := []zap.Option{
		zap.AddStacktrace(stacktraceLevel),
		zap.AddCaller(),
	}
	if development {
		options = append(options, zap.Development())
	}

	logger := zap.New(
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level),
		options...,
	)
	if cfg.Service != "" {
		logger = logger.With(zap.String("service", cfg.Service))
	}
	defaultLogger.Store(logger)
	zap.ReplaceGlobals(logger)

	return logger, nil
}

// L 返回当前全局 logger。业务代码记**请求级**日志请用 FromContext(ctx)，
// 它会自动带上 trace_id；只有进程级 / 启动期日志才直接调 L。
func L() *zap.Logger {
	return defaultLogger.Load()
}

// SetLogger 替换全局 logger，返回一个 restore 函数。**主要给测试用**——
// 测试里 defer SetLogger(zap.NewNop())() 一行静音整个 audit log 流。
func SetLogger(logger *zap.Logger) func() {
	if logger == nil {
		logger = zap.NewNop()
	}

	previous := defaultLogger.Load()
	defaultLogger.Store(logger)
	zap.ReplaceGlobals(logger)

	return func() {
		defaultLogger.Store(previous)
		zap.ReplaceGlobals(previous)
	}
}

// Sync flush 缓冲日志条目；cmd/*/main.go defer Sync()，让程序退出前最后一
// 批日志能落盘 / 落 stdout（stdout 走 pipe 时会被缓冲）。
func Sync() error {
	return defaultLogger.Load().Sync()
}

// WithTraceID 返回带 trace_id 的新 ctx。中间件 TraceLogger 已经调过它，业
// 务代码通常不直接用。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, traceID)
}

// TraceIDFrom 从 ctx 取 trace_id；没有返空串。让 service / repository 在
// 透传给 task payload 时能直接用。
func TraceIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// EnsureTraceID 在 ctx 没 trace_id 时挂一个。worker 消费任务时用：HTTP
// 链路传过来的 trace_id 已经在 ctx 里就复用；没有就用合成 trace_id 兜底。
func EnsureTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if TraceIDFrom(ctx) != "" {
		return ctx
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return ctx
	}
	return WithTraceID(ctx, traceID)
}

// NewTraceID 用冒号拼接稳定的 trace 片段。worker 没拿到 HTTP trace 时，用
// "asynq:<task_id>" 这种形式合成一个，让日志至少可关联到任务。
func NewTraceID(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ":")
}

// FromContext 返回一个已经预绑 trace_id 字段的 zap logger。business 层
// （service / repository / handler）打日志都应该走这条，免去每条手动加
// trace_id 字段。
func FromContext(ctx context.Context) *zap.Logger {
	entry := defaultLogger.Load()
	if traceID := TraceIDFrom(ctx); traceID != "" {
		entry = entry.With(zap.String("trace_id", traceID))
	}
	return entry
}

// Error 是 zap.Error 的薄重导出，让调用方少 import 一个包名。
func Error(err error) zap.Field {
	return zap.Error(err)
}

// parseLevel 把字符串 level 翻译成 zapcore.Level。空串当 "info" 处理。
func parseLevel(level string) (zapcore.Level, error) {
	var parsed zapcore.Level
	if level == "" {
		level = "info"
	}
	if err := parsed.Set(level); err != nil {
		return zapcore.InfoLevel, fmt.Errorf("parse log level %q: %w", level, err)
	}
	return parsed, nil
}
