package metrics

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// asyncQueueInspector 是 asynq.Inspector 的最小化接口，便于测试 mock。
// 实际生产用 *asynq.Inspector。
type asyncQueueInspector interface {
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
}

// asynqMetrics 持有 worker 队列相关 gauge。labels=queue 让 Prometheus 能按
// 队列名分组查（critical / default / low 等）。
type asynqMetrics struct {
	size      *prometheus.GaugeVec
	pending   *prometheus.GaugeVec
	active    *prometheus.GaugeVec
	retry     *prometheus.GaugeVec
	archived  *prometheus.GaugeVec
	latencySec *prometheus.GaugeVec
}

// withAsynqMetrics 给 Registry 挂上 asynq 相关 gauge。在 New 内部一次性注册。
func newAsynqMetrics(reg *prometheus.Registry, subsystem string) *asynqMetrics {
	m := &asynqMetrics{
		size:       newQueueGauge(subsystem, "asynq_queue_size", "Total number of tasks in the queue (pending + active + scheduled + retry + archived)."),
		pending:    newQueueGauge(subsystem, "asynq_queue_pending", "Number of pending tasks waiting to be picked up by a worker."),
		active:     newQueueGauge(subsystem, "asynq_queue_active", "Number of tasks currently being processed."),
		retry:      newQueueGauge(subsystem, "asynq_queue_retry", "Number of tasks scheduled for retry after a failure."),
		archived:   newQueueGauge(subsystem, "asynq_queue_archived", "Number of tasks that exhausted retries and moved to archived (dead-letter)."),
		latencySec: newQueueGauge(subsystem, "asynq_queue_latency_seconds", "Age of the oldest pending task in the queue (a long latency means consumer is falling behind)."),
	}
	reg.MustRegister(m.size, m.pending, m.active, m.retry, m.archived, m.latencySec)
	return m
}

func newQueueGauge(subsystem, name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "go_skeleton",
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, []string{"queue"})
}

// StartAsynqCollector 起一个后台 goroutine，每 interval 抓一次 inspector
// 的队列状态填进 gauge。ctx 取消时退出，保证 server.Shutdown 不留 goroutine。
//
// queues 是要观测的队列名清单，从 config.Worker.Queues map 的 key 来。
// inspector 由 caller 负责构造和关闭——这里只是消费它，不接管生命周期。
// logger 用业务侧已有的 zap，抓数据失败时 warn（不致命）。
//
// interval <= 0 时退化到 30s。30s 是 Prometheus 默认 scrape 周期的常见值，
// 抓得更勤会给 Redis 添无谓负载；更稀疏又会让 alert 反应滞后。
func (r *Registry) StartAsynqCollector(ctx context.Context, inspector asyncQueueInspector, queues []string, interval time.Duration, logger *zap.Logger) {
	if r == nil || r.asynq == nil || inspector == nil || len(queues) == 0 {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// 起步先抓一次，让首次 scrape 不至于是空数据。
		r.collectAsynq(inspector, queues, logger)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.collectAsynq(inspector, queues, logger)
			}
		}
	}()
}

func (r *Registry) collectAsynq(inspector asyncQueueInspector, queues []string, logger *zap.Logger) {
	for _, name := range queues {
		info, err := inspector.GetQueueInfo(name)
		if err != nil {
			logger.Warn("asynq queue inspect failed", zap.String("queue", name), zap.Error(err))
			continue
		}
		r.asynq.size.WithLabelValues(name).Set(float64(info.Size))
		r.asynq.pending.WithLabelValues(name).Set(float64(info.Pending))
		r.asynq.active.WithLabelValues(name).Set(float64(info.Active))
		r.asynq.retry.WithLabelValues(name).Set(float64(info.Retry))
		r.asynq.archived.WithLabelValues(name).Set(float64(info.Archived))
		r.asynq.latencySec.WithLabelValues(name).Set(info.Latency.Seconds())
	}
}
