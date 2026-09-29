package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// unknownTaskType 是未在 mux 注册的 task type 的统一 label：这类任务走 asynq
// NotFoundHandler，type 由生产方任意决定，原样当 label 会让基数失控。
const unknownTaskType = "unknown"

// TaskObserver 是任务级指标的记录契约；生产实现是 *metrics.Registry
// （pkg/metrics.ObserveTask）。worker 包只依赖这个接口，测试可注入手写 mock。
type TaskObserver interface {
	ObserveTask(taskType string, err error, d time.Duration)
}

// MetricsMiddleware 记录每个任务的处理结果与耗时。handler 的 error 原样透传
// ——吞掉会让 asynq 以为成功、不再重试。
//
// 记录放在 defer 里并 recover：asynq 在 middleware 链之外（processor）recover
// panic，不 defer 的话 panic 任务根本不会被计数，最严重的失败反而不可见。
// panic 时记一次 failure 后原样 re-panic，保证 asynq 的 recover / 重试照常。
//
// label 把 task 映射成指标 type label；nil 时直接用 t.Type()。obs 为 nil 时
// 直接透传，不做任何记录。
func MetricsMiddleware(obs TaskObserver, label func(*asynq.Task) string) asynq.MiddlewareFunc {
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
				if r := recover(); r != nil {
					obs.ObserveTask(label(t), fmt.Errorf("panic: %v", r), time.Since(start))
					panic(r)
				}
				obs.ObserveTask(label(t), err, time.Since(start))
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
