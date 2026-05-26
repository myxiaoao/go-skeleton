// Package metrics 提供 Prometheus 指标收集与暴露。
//
// 设计取舍：
//   - 单 Registry 实例，所有 collector 在 New 时一次性注册；调用方拿 *Registry
//     就能挂 middleware + 暴露 /metrics，不再有"哪些 collector 被注册"的歧义。
//   - 不暴露 prometheus.Registerer / Collector 给外层，避免业务代码到处自建
//     collector 造成 cardinality 失控。需要业务指标时在本包加 Observe 方法。
//   - 不引入 OpenTelemetry：先 Prometheus pull 模式够内部 SRE 用，等真要做
//     trace 时再换底。
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry 收拢业务 collector + 默认 Go runtime / process collector。
//
// 用独立 Registry（不复用 prometheus.DefaultRegisterer）的原因：
//   - 多实例进程（API + Worker）跑在同机时，全局 Registerer 会撞名；
//   - 测试可以构造独立 Registry，避免跨用例污染。
type Registry struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inflight prometheus.Gauge
	asynq    *asynqMetrics
}

// New 构造 Registry 并预注册标准 collector + 业务 collector。subsystem 用
// 来区分 API / Worker 进程的指标命名空间（最终指标名形如
// go_skeleton_<subsystem>_http_requests_total），让 Prometheus 用 job 标签
// 之外再多一个区分维度，避免误聚合。
func New(subsystem string) *Registry {
	reg := prometheus.NewRegistry()

	// Go runtime（goroutines / GC / memstats）+ process（fd / cpu / rss）
	// 两组指标是 SRE 排障的标配，全引上几乎没成本。
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "go_skeleton",
		Subsystem: subsystem,
		Name:      "http_requests_total",
		Help:      "Total HTTP requests partitioned by method, route, HTTP status, and business code.",
	}, []string{"method", "route", "status", "code"})

	// Histogram buckets 选 5ms / 10ms / 25ms / 50ms / 100ms / 250ms / 500ms /
	// 1s / 2.5s / 5s / 10s。覆盖从内网快返到外部依赖慢响应的常见区间；
	// 业务自定义指标若需要不同分布，请新建 collector，不要改这里。
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "go_skeleton",
		Subsystem: subsystem,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "route", "status", "code"})

	inflight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "go_skeleton",
		Subsystem: subsystem,
		Name:      "http_requests_in_flight",
		Help:      "Number of HTTP requests currently being processed.",
	})

	reg.MustRegister(requests, duration, inflight)

	return &Registry{
		reg:      reg,
		requests: requests,
		duration: duration,
		inflight: inflight,
		asynq:    newAsynqMetrics(reg, subsystem),
	}
}

// Handler 返回符合 Prometheus 抓取格式的 http.Handler。挂到 gin 的方式：
//
//	engine.GET("/metrics", gin.WrapH(metrics.Handler()))
//
// 故意**不**走 BearerAuth：metrics 端点应该让 Prometheus / Grafana Agent
// 抓，不应该绑业务身份。生产环境靠网络层（不暴露公网 + LB allowlist）保护。
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		// EnableOpenMetrics 让响应同时支持 OpenMetrics 1.0 协议
		// （Prometheus / Grafana Agent 都识别），向前兼容老 Prometheus
		// 的 text/plain 抓取，没有副作用。
		EnableOpenMetrics: true,
	})
}

// HTTPMiddleware 给 gin engine 套上指标观测。c.FullPath() 返回路由模板
// （例如 /api/v1/examples/:id），不是裸 URL，避免 path 高基数把内存撑爆。
// 路由未命中（404）时 FullPath 为空，标记成 "not_found" 兜底。
//
// code label 取自 gin.Context 的 "response_code" key（由 pkg/response 的
// WriteSuccess / WriteError / WriteValidationError 提前 Set 过）。这避免
// 了 wrap response writer 抓 body 解 JSON 的开销，也避免 metrics 反向
// import response 形成循环依赖。没拿到值（如健康检查路径、未走 response
// 包的 handler、或路由 404）兜底 "0"——表示"没有业务 code 信息"。
func (r *Registry) HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		r.inflight.Inc()
		start := time.Now()

		c.Next()

		r.inflight.Dec()

		route := c.FullPath()
		if route == "" {
			route = "not_found"
		}
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		code := businessCodeLabel(c)

		r.requests.WithLabelValues(method, route, status, code).Inc()
		r.duration.WithLabelValues(method, route, status, code).Observe(time.Since(start).Seconds())
	}
}

// businessCodeLabel 从 gin.Context 取业务 code 写成 Prometheus label 字符串。
// 走过 pkg/response 的请求会有值；没走（如 /health 直接 c.JSON、404 路由）
// 返 "0" 表示"没有业务码信息"，让 label 始终非空、避免 cardinality 抖动。
func businessCodeLabel(c *gin.Context) string {
	v, ok := c.Get("response_code")
	if !ok {
		return "0"
	}
	switch code := v.(type) {
	case int:
		return strconv.Itoa(code)
	case int64:
		return strconv.FormatInt(code, 10)
	case string:
		if code == "" {
			return "0"
		}
		return code
	default:
		return "0"
	}
}
