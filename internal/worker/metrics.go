package worker

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
)

// TaskObserver 是任务级指标的记录契约；生产实现是 *metrics.Registry
// （pkg/metrics.ObserveTask）。worker 包只依赖这个接口，测试可注入手写 mock。
type TaskObserver interface {
	ObserveTask(taskType string, err error, d time.Duration)
}

// MetricsMiddleware 记录每个任务的处理结果与耗时。handler 的 error 原样透传
// ——吞掉会让 asynq 以为成功、不再重试。obs 为 nil 时直接透传，不做任何记录。
func MetricsMiddleware(obs TaskObserver) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		if obs == nil {
			return next
		}
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			start := time.Now()
			err := next.ProcessTask(ctx, t)
			obs.ObserveTask(t.Type(), err, time.Since(start))
			return err
		})
	}
}

// registerMetricsMiddleware 在 obs 非 nil 时把 MetricsMiddleware 挂到 mux 上；
// 未开指标（obs 为 nil）时不挂，避免每个任务多一层空包装。
func registerMetricsMiddleware(mux *asynq.ServeMux, obs TaskObserver) {
	if mux != nil && obs != nil {
		mux.Use(MetricsMiddleware(obs))
	}
}
