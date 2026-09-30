package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"go-skeleton/pkg/metrics"
)

// unknownTaskType 是未在 mux 注册的 task type 的统一 label：这类任务走 asynq
// NotFoundHandler，type 由生产方任意决定，原样当 label 会让基数失控。
const unknownTaskType = "unknown"

// TaskObserver 是任务级指标的记录契约；生产实现是 *metrics.Registry
// （pkg/metrics.ObserveTask）。worker 包只依赖这个接口，测试可注入手写 mock。
type TaskObserver interface {
	ObserveTask(taskType, status string, d time.Duration)
}

// retryInfoFunc 取 asynq 注入 ctx 的重试信息：已重试次数、最大重试次数、是否存在。
// 抽成函数类型是因为 asynq 的 ctx 元数据无法在包外构造，测试需要注入。
type retryInfoFunc func(ctx context.Context) (retried, maxRetry int, ok bool)

func asynqRetryInfo(ctx context.Context) (int, int, bool) {
	retried, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	return retried, maxRetry, ok1 && ok2
}

// taskStatus 把一次处理结果映射成互斥的 status：
//   - err == nil → success；
//   - SkipRetry / RevokeTask，或重试预算耗尽（retried >= maxRetry，与 asynq
//     processor 的判定一致）→ failure（最终失败，不会再执行）；
//   - 其余 → retry。拿不到重试信息（非 asynq 上下文）时保守按 retry。
//
// 以 handler 返回值为准：任务超时 / lease 过期由 processor 独立判定失败，handler
// 忽略 ctx 仍返回 nil 时这里会记 success；停机 abort 走 requeue、不消耗重试预算，
// 最后一次尝试恰逢停机时这里会记 failure 而任务之后仍会再跑。两者都是少见边界。
func taskStatus(err error, retried, maxRetry int, ok bool) string {
	switch {
	case err == nil:
		return metrics.TaskStatusSuccess
	case errors.Is(err, asynq.SkipRetry), errors.Is(err, asynq.RevokeTask):
		return metrics.TaskStatusFailure
	case ok && retried >= maxRetry:
		return metrics.TaskStatusFailure
	default:
		return metrics.TaskStatusRetry
	}
}

// MetricsMiddleware 记录每个任务的处理结果与耗时。handler 的 error 原样透传
// ——吞掉会让 asynq 以为成功、不再重试。
//
// 记录放在 defer 里并 recover：asynq 在 middleware 链之外（processor）recover
// panic，不 defer 的话 panic 任务根本不会被计数，最严重的失败反而不可见。
// panic 按 error 处理（最后一次重试才算 failure）后原样 re-panic，保证 asynq
// 的 recover / 重试照常。
//
// label 把 task 映射成指标 type label；nil 时直接用 t.Type()。obs 为 nil 时
// 直接透传，不做任何记录。
func MetricsMiddleware(obs TaskObserver, label func(*asynq.Task) string) asynq.MiddlewareFunc {
	return newMetricsMiddleware(obs, label, asynqRetryInfo)
}

func newMetricsMiddleware(obs TaskObserver, label func(*asynq.Task) string, info retryInfoFunc) asynq.MiddlewareFunc {
	if label == nil {
		label = (*asynq.Task).Type
	}
	return func(next asynq.Handler) asynq.Handler {
		if obs == nil {
			return next
		}
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) (err error) {
			start := time.Now()
			defer func() {
				r := recover()
				if r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
				retried, maxRetry, ok := info(ctx)
				obs.ObserveTask(label(t), taskStatus(err, retried, maxRetry, ok), time.Since(start))
				if r != nil {
					panic(r)
				}
			}()
			return next.ProcessTask(ctx, t)
		})
	}
}

// registerMetricsMiddleware 在 obs 非 nil 时把 MetricsMiddleware 挂到 mux 上；
// 未开指标（obs 为 nil）时不挂，避免每个任务多一层空包装。type label 取 mux
// 上匹配到的注册 pattern，未注册的 type 统一记成 "unknown"。
func registerMetricsMiddleware(mux *asynq.ServeMux, obs TaskObserver) {
	if mux != nil && obs != nil {
		mux.Use(MetricsMiddleware(obs, muxPatternLabel(mux)))
	}
}

// muxPatternLabel 用 mux.Handler 查注册 pattern 作为 label。mux.Handler 只在
// 查找期间持读锁、handler 执行时锁已释放，在 middleware 里调用不会死锁。
func muxPatternLabel(mux *asynq.ServeMux) func(*asynq.Task) string {
	return func(t *asynq.Task) string {
		if _, pattern := mux.Handler(t); pattern != "" {
			return pattern
		}
		return unknownTaskType
	}
}
