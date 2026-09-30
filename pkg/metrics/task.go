package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// taskMetrics 持有 worker 消费端的任务级指标：按 task type 统计处理结果与耗时。
// label 只用 type / status。type 由调用方收敛到低基数集合（worker 侧取 mux
// 注册 pattern，未注册的 type 统一记 "unknown"，见 internal/worker/metrics.go），
// 不要直接用生产方传入的任意字符串；也不要加 task_id / 业务 ID 这类高基数维度。
type taskMetrics struct {
	processed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
}

func newTaskMetrics(reg *prometheus.Registry, subsystem string) *taskMetrics {
	m := &taskMetrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "go_skeleton",
			Subsystem: subsystem,
			Name:      "asynq_tasks_processed_total",
			Help:      "Total asynq tasks processed by this worker, partitioned by task type and status (success|retry|failure). failure means final failure (no more retries), including tasks revoked via asynq.RevokeTask, so it may exceed the archived count.",
		}, []string{"type", "status"}),
		// 后台任务耗时分布比 HTTP 请求宽得多：从几十毫秒的轻量任务到分钟级的
		// 批处理都有，所以桶从 10ms 一路铺到 5min。
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "go_skeleton",
			Subsystem: subsystem,
			Name:      "asynq_task_duration_seconds",
			Help:      "Asynq task processing duration in seconds.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"type"}),
	}
	reg.MustRegister(m.processed, m.duration)
	return m
}

// 任务处理结果的 status label 取值，互斥：
//   - success：处理成功；
//   - retry：本次失败但 asynq 还会重试；
//   - failure：最终失败（重试耗尽 / SkipRetry / RevokeTask），不会再被执行。
const (
	TaskStatusSuccess = "success"
	TaskStatusRetry   = "retry"
	TaskStatusFailure = "failure"
)

// ObserveTask 记录一次任务处理结果：status 取上面的 TaskStatus* 常量，由调用方
// （worker middleware，持有 asynq 重试上下文）判定；d 计入耗时 histogram。
// 未知 status 一律折叠成 failure，避免任意字符串撑爆 label 基数。
// nil *Registry 安全调用（no-op），调用方不必判空。
func (r *Registry) ObserveTask(taskType, status string, d time.Duration) {
	if r == nil || r.tasks == nil {
		return
	}
	switch status {
	case TaskStatusSuccess, TaskStatusRetry, TaskStatusFailure:
	default:
		status = TaskStatusFailure
	}
	r.tasks.processed.WithLabelValues(taskType, status).Inc()
	r.tasks.duration.WithLabelValues(taskType).Observe(d.Seconds())
}
