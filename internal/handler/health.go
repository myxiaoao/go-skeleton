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

// buildResponse mirrors the anonymous struct on oapi.HealthResponse.Build
// so the handler doesn't construct it inline three times.
func buildResponse() struct {
	BuildTime string `json:"build_time"`
	Commit    string `json:"commit"`
	Version   string `json:"version"`
} {
	return struct {
		BuildTime string `json:"build_time"`
		Commit    string `json:"commit"`
		Version   string `json:"version"`
	}{
		BuildTime: buildinfo.BuildTime,
		Commit:    buildinfo.Commit,
		Version:   buildinfo.Version,
	}
}

// HealthHandler checks infrastructure dependencies.
type HealthHandler struct {
	db       *database.DBManager
	cache    *cache.Client
	draining *atomic.Bool
}

// NewHealthHandler creates a HealthHandler. draining 可为 nil（不参与 graceful drain 的进程）。
func NewHealthHandler(db *database.DBManager, cache *cache.Client, draining *atomic.Bool) *HealthHandler {
	return &HealthHandler{db: db, cache: cache, draining: draining}
}

// Live is the liveness probe — succeeds as long as the process can serve a
// request. Must not touch DB / Redis: a failure here causes Kubernetes to
// restart the pod, which is the wrong response when downstreams flap.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, oapi.LivenessResponse{
		Status:  "ok",
		Version: buildinfo.Version,
	})
}

// Health is the readiness probe — returns 503 when required dependencies
// are unavailable, so the load balancer can pull the pod out of rotation
// without restarting it. The response shape is pinned to oapi.HealthResponse
// so it stays aligned with api/openapi.yaml.
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
	healthy := true

	switch {
	case h.db == nil:
		checks["postgres"] = oapi.HealthResponseChecksNotConfigured
		healthy = false
	case h.db.Ping(ctx) != nil:
		checks["postgres"] = oapi.HealthResponseChecksUnavailable
		healthy = false
	default:
		checks["postgres"] = oapi.HealthResponseChecksOk
	}

	switch {
	case h.cache == nil:
		checks["redis"] = oapi.HealthResponseChecksNotConfigured
	case h.cache.Ping(ctx) != nil:
		checks["redis"] = oapi.HealthResponseChecksUnavailable
		healthy = false
	default:
		checks["redis"] = oapi.HealthResponseChecksOk
	}

	status := oapi.HealthResponseStatusOk
	httpStatus := http.StatusOK
	if !healthy {
		status = oapi.HealthResponseStatusUnhealthy
		httpStatus = http.StatusServiceUnavailable
	}

	c.JSON(httpStatus, oapi.HealthResponse{
		Status: status,
		Checks: checks,
		Build:  buildResponse(),
	})
}
