package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
	"go-skeleton/pkg/cache"
	"go-skeleton/pkg/database"
)

// HealthHandler checks infrastructure dependencies.
type HealthHandler struct {
	db    *database.DBManager
	cache *cache.Client
}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler(db *database.DBManager, cache *cache.Client) *HealthHandler {
	return &HealthHandler{db: db, cache: cache}
}

// Live is the liveness probe — succeeds as long as the process can serve a
// request. Must not touch DB / Redis: a failure here causes Kubernetes to
// restart the pod, which is the wrong response when downstreams flap.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, oapi.LivenessResponse{Status: "ok"})
}

// Health is the readiness probe — returns 503 when required dependencies
// are unavailable, so the load balancer can pull the pod out of rotation
// without restarting it. The response shape is pinned to oapi.HealthResponse
// so it stays aligned with api/openapi.yaml.
func (h *HealthHandler) Health(c *gin.Context) {
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
	})
}
