package worker

import (
	"context"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/task"
	applog "go-skeleton/pkg/log"
)

// ServerConfig 把 Asynq 调参集中成一个入参对象。默认值由 config.Load 在对
// 应 env 缺失时补；这里只做透传，不再 fallback。
type ServerConfig struct {
	Concurrency    int
	Queues         map[string]int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
}

// NewServer 按 ServerConfig 构造 asynq worker server。RetryDelayFunc 走自定
// 义的 computeRetryDelay（带溢出守卫），ErrorHandler 把失败任务串到 zap 日志。
func NewServer(redisOpt asynq.RedisClientOpt, sc ServerConfig) *asynq.Server {
	baseDelay := sc.RetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}
	maxDelay := sc.RetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = time.Hour
	}

	return asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: sc.Concurrency,
		Queues:      sc.Queues,
		RetryDelayFunc: func(n int, e error, t *asynq.Task) time.Duration {
			delay := computeRetryDelay(n, baseDelay, maxDelay)
			logger := applog.L()
			if traceID := task.TraceIDFromPayload(t.Payload()); traceID != "" {
				logger = logger.With(zap.String("trace_id", traceID))
			}
			logger.Warn("asynq task will retry",
				zap.String("task", t.Type()),
				zap.Int("attempt", n+1),
				zap.Duration("retry_after", delay),
				zap.Error(e),
			)
			return delay
		},
		ErrorHandler: asynq.ErrorHandlerFunc(logTaskFailed),
	})
}

// computeRetryDelay 计算 asynq retry 的指数 backoff：min(2^n * base, max)。
//
// 防溢出：time.Duration 是 int64 纳秒，2^n 在 n 增长时会很快超过 int64 范围；
// 一旦溢出 `1<<n` 变负数，乘以 baseDelay 后 `delay > maxDelay` 比较失败
// （负数小于正数），返回负 duration 给 asynq 会触发未定义行为。所以一旦
// n 大到指数会溢出，直接返回 maxDelay 兜底。
//
// 阈值 30：base=5s 时 2^30*5s ≈ 170 年远超 maxDelay=1h；asynq 默认 maxRetry=25
// 已经够富余。下界 0 是合法的（首次失败 attempt=0）。
func computeRetryDelay(n int, base, max time.Duration) time.Duration {
	if n < 0 {
		n = 0
	}
	if n >= 30 {
		return max
	}
	delay := time.Duration(1<<n) * base
	if delay <= 0 || delay > max {
		return max
	}
	return delay
}

type taskRuntimeMetadata struct {
	TaskID             string
	Queue              string
	RetryCount         int
	RetryCountRecorded bool
}

type taskRuntimeMetadataProvider func(context.Context) taskRuntimeMetadata

func taskRuntimeMetadataFromContext(ctx context.Context) taskRuntimeMetadata {
	meta := taskRuntimeMetadata{}
	if taskID, ok := asynq.GetTaskID(ctx); ok {
		meta.TaskID = taskID
	}
	if queue, ok := asynq.GetQueueName(ctx); ok {
		meta.Queue = queue
	}
	if retryCount, ok := asynq.GetRetryCount(ctx); ok {
		meta.RetryCount = retryCount
		meta.RetryCountRecorded = true
	}
	return meta
}

// TraceMiddleware 从 task payload 里恢复 API 端写入的 trace_id（NewExampleTask
// 会把当前 ctx 的 trace_id 写进 payload），并打 task 生命周期日志，让队列
// 消费端跟 HTTP 链路用同一个 trace_id 串起来。
func TraceMiddleware(next asynq.Handler) asynq.Handler {
	return traceMiddleware(next, taskRuntimeMetadataFromContext)
}

func traceMiddleware(next asynq.Handler, metaProvider taskRuntimeMetadataProvider) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		ctx, fields := taskLogContext(ctx, t, metaProvider(ctx))
		applog.FromContext(ctx).Info("asynq task started", fields...)
		err := next.ProcessTask(ctx, t)
		if err != nil {
			return err
		}
		applog.FromContext(ctx).Info("asynq task finished", fields...)
		return nil
	})
}

func logTaskFailed(ctx context.Context, t *asynq.Task, err error) {
	ctx, fields := taskLogContext(ctx, t, taskRuntimeMetadataFromContext(ctx))
	fields = append(fields, zap.Error(err))
	applog.FromContext(ctx).Error("asynq task failed", fields...)
}

func taskLogContext(ctx context.Context, t *asynq.Task, meta taskRuntimeMetadata) (context.Context, []zap.Field) {
	traceID := task.TraceIDFromPayload(t.Payload())
	traceSource := "request"
	if traceID == "" {
		traceID = applog.NewTraceID("asynq", meta.TaskID)
		traceSource = "asynq_task"
	}
	ctx = applog.EnsureTraceID(ctx, traceID)

	fields := []zap.Field{
		zap.String("task", t.Type()),
		zap.String("trace_source", traceSource),
	}
	if strings.TrimSpace(meta.TaskID) != "" {
		fields = append(fields, zap.String("task_id", meta.TaskID))
	}
	if strings.TrimSpace(meta.Queue) != "" {
		fields = append(fields, zap.String("queue", meta.Queue))
	}
	if meta.RetryCountRecorded {
		fields = append(fields, zap.Int("retry_count", meta.RetryCount))
	}
	return ctx, fields
}

func registerTraceMiddleware(mux *asynq.ServeMux) {
	if mux != nil {
		mux.Use(TraceMiddleware)
	}
}
