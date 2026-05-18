package handler

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
	"go-skeleton/pkg/buildinfo"
	"go-skeleton/pkg/cache"
	"go-skeleton/pkg/database"
)

// healthDBPinger / healthCachePinger 让 /health 可测；生产实现是
// *database.DBManager / *cache.Client，测试里可注入桩。
type healthDBPinger interface {
	Ping(context.Context) error
}

type healthCachePinger interface {
	Ping(context.Context) error
}

// HealthHandler 实现 K8s 风格的探活：/livez（liveness）+ /health（readiness）。
// 持有 db / cache 的 ping 接口和 draining 信号；后者由 main 在 SIGTERM 时翻
// true，用来让 /health 提前返 503 让 LB 摘流。
type HealthHandler struct {
	db       healthDBPinger
	cache    healthCachePinger
	draining *atomic.Bool
}

// NewHealthHandler 构造 HealthHandler。db 为 nil 表示 not_configured；
// cache 为 nil 同义。draining 可为 nil（不参与 graceful drain 的进程）。
func NewHealthHandler(db *database.DBManager, cache *cache.Client, draining *atomic.Bool) *HealthHandler {
	h := &HealthHandler{draining: draining}
	// 避免 typed-nil 把接口包成 non-nil。
	if db != nil {
		h.db = db
	}
	if cache != nil {
		h.cache = cache
	}
	return h
}

// Live 是 liveness 探针——只要进程还能接请求就返 200。
//
// 故意不碰 DB / Redis：liveness 失败会让 K8s 重启 Pod，而下游抖动时重启
// 是错的响应（重启不能让外部 DB 复活，反而扩大故障面）。下游探活属于
// readiness（/health）的职责。
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, oapi.LivenessResponse{
		Status:  "ok",
		Version: buildinfo.Version,
	})
}

// Health 是 readiness 探针——关键依赖不可用时返 503，LB 借此把 Pod 摘出
// 轮询而不重启它（区别于 liveness）。响应结构绑死到 oapi.HealthResponse，
// 保证和 api/openapi.yaml 不漂移。
func (h *HealthHandler) Health(c *gin.Context) {
	// graceful drain：SIGTERM 后 main 先翻 draining=true，让 LB 在 GracefulDrain
	// 窗口内摘流，再退出 HTTP server，避免请求被半途切断。
	if h.draining != nil && h.draining.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "draining"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]oapi.HealthResponseChecks{}
	// 关键依赖（Postgres）挂 → unhealthy → 503，LB 必须摘流。
	// 非关键依赖（Redis）挂 → degraded → 200，LB 不摘流（缓存抖动不应触发全员重启）。
	dbHealthy := true
	cacheHealthy := true

	switch {
	case h.db == nil:
		checks["postgres"] = oapi.HealthResponseChecksNotConfigured
		dbHealthy = false
	case h.db.Ping(ctx) != nil:
		checks["postgres"] = oapi.HealthResponseChecksUnavailable
		dbHealthy = false
	default:
		checks["postgres"] = oapi.HealthResponseChecksOk
	}

	switch {
	case h.cache == nil:
		checks["redis"] = oapi.HealthResponseChecksNotConfigured
	case h.cache.Ping(ctx) != nil:
		checks["redis"] = oapi.HealthResponseChecksUnavailable
		cacheHealthy = false
	default:
		checks["redis"] = oapi.HealthResponseChecksOk
	}

	status := oapi.HealthResponseStatusOk
	httpStatus := http.StatusOK
	switch {
	case !dbHealthy:
		status = oapi.HealthResponseStatusUnhealthy
		httpStatus = http.StatusServiceUnavailable
	case !cacheHealthy:
		status = oapi.HealthResponseStatusDegraded
		// httpStatus 保持 200：让 LB 区别 "完全挂" 和 "降级运行"。
	}

	resp := oapi.HealthResponse{
		Status: status,
		Checks: checks,
	}
	resp.Build.BuildTime = buildinfo.BuildTime
	resp.Build.Commit = buildinfo.Commit
	resp.Build.Version = buildinfo.Version
	c.JSON(httpStatus, resp)
}
