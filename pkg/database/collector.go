package database

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolCollector 是 pgxpool.Pool 的 Prometheus 读时（scrape-time）collector：
// 每次 /metrics 抓取都现读一次 pool.Stat() 快照，不额外起 goroutine、不缓存
// 陈旧数据，也不用担心进程退出时忘记停协程。
//
// 指标名固定带 go_skeleton_db_pool_ 前缀，不按 API / Worker 拆 subsystem——
// 两个进程各自持有独立的 prometheus.Registry 实例（见 pkg/metrics.New），
// 同名指标不会在同一份 /metrics 输出里撞车，也就没必要多引入一维区分。
type poolCollector struct {
	pool *pgxpool.Pool

	acquiredConns   *prometheus.Desc
	idleConns       *prometheus.Desc
	totalConns      *prometheus.Desc
	maxConns        *prometheus.Desc
	acquireCount    *prometheus.Desc
	emptyAcquire    *prometheus.Desc
	canceledAcquire *prometheus.Desc
	acquireDuration *prometheus.Desc
}

// Collector 返回连接池状态的 Prometheus collector，供 internal/server.go /
// 未来的 internal/worker.go 通过 metrics.Registry.MustRegister 挂载。
//
// pool 未配置（DSN 为空、或 DBManager 为 nil）时返回 nil——调用方据此跳过
// 注册，语义是"没有 DB 就不产出这组指标"，而不是注册一个恒为 0 的假
// collector 误导 SRE 排障。
func (m *DBManager) Collector() prometheus.Collector {
	if m == nil || m.pool == nil {
		return nil
	}
	return &poolCollector{
		pool: m.pool,

		acquiredConns: prometheus.NewDesc(
			"go_skeleton_db_pool_acquired_conns",
			"Number of currently acquired (in-use) connections in the pool.",
			nil, nil,
		),
		idleConns: prometheus.NewDesc(
			"go_skeleton_db_pool_idle_conns",
			"Number of currently idle connections in the pool.",
			nil, nil,
		),
		totalConns: prometheus.NewDesc(
			"go_skeleton_db_pool_total_conns",
			"Total number of connections currently in the pool (acquired + idle + constructing).",
			nil, nil,
		),
		maxConns: prometheus.NewDesc(
			"go_skeleton_db_pool_max_conns",
			"Maximum size of the pool (from DATABASE_MAX_CONNS).",
			nil, nil,
		),
		acquireCount: prometheus.NewDesc(
			"go_skeleton_db_pool_acquire_count_total",
			"Cumulative count of successful acquires from the pool.",
			nil, nil,
		),
		emptyAcquire: prometheus.NewDesc(
			"go_skeleton_db_pool_empty_acquire_count_total",
			"Cumulative count of successful acquires that had to wait because the pool was empty.",
			nil, nil,
		),
		canceledAcquire: prometheus.NewDesc(
			"go_skeleton_db_pool_canceled_acquire_count_total",
			"Cumulative count of acquires canceled by the caller's context.",
			nil, nil,
		),
		acquireDuration: prometheus.NewDesc(
			"go_skeleton_db_pool_acquire_duration_seconds_total",
			"Cumulative duration spent waiting on successful acquires from the pool, in seconds.",
			nil, nil,
		),
	}
}

// Describe 实现 prometheus.Collector。
func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquiredConns
	ch <- c.idleConns
	ch <- c.totalConns
	ch <- c.maxConns
	ch <- c.acquireCount
	ch <- c.emptyAcquire
	ch <- c.canceledAcquire
	ch <- c.acquireDuration
}

// Collect 实现 prometheus.Collector。每次抓取现读 pool.Stat()，顺序与
// Describe 一致（测试依赖这个顺序做白盒断言，改动请同步更新）。
func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()

	ch <- prometheus.MustNewConstMetric(c.acquiredConns, prometheus.GaugeValue, float64(stat.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idleConns, prometheus.GaugeValue, float64(stat.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.totalConns, prometheus.GaugeValue, float64(stat.TotalConns()))
	ch <- prometheus.MustNewConstMetric(c.maxConns, prometheus.GaugeValue, float64(stat.MaxConns()))
	ch <- prometheus.MustNewConstMetric(c.acquireCount, prometheus.CounterValue, float64(stat.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.emptyAcquire, prometheus.CounterValue, float64(stat.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.canceledAcquire, prometheus.CounterValue, float64(stat.CanceledAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stat.AcquireDuration().Seconds())
}
