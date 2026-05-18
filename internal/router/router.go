package router

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/internal/handler"
)

// Dependencies collects handlers and middleware needed during route registration.
type Dependencies struct {
	Auth         *handler.AuthHandler
	AuthRequired gin.HandlerFunc
	Example      *handler.ExampleHandler
}

// RegisterRoutes registers API routes under the given router group.
func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	registerAuthRoutes(r, deps)
	registerExampleRoutes(r, deps)
	return nil
}

func registerAuthRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.Auth == nil {
		return
	}

	authRoutes := r.Group("/auth")
	// POST /auth/token is always registered so the OpenAPI spec and runtime
	// routes match. When the JWT manager is missing or the dev-token endpoint
	// is disabled, the handler returns SERVICE_DISABLED instead of 404 (see
	// AuthHandler.CreateToken).
	authRoutes.POST("/token", deps.Auth.CreateToken)
	if deps.AuthRequired != nil {
		authRoutes.GET("/me", deps.AuthRequired, deps.Auth.Me)
	}
}

func registerExampleRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.Example == nil {
		return
	}

	examples := r.Group("/examples")
	examples.GET("", deps.Example.List)
	examples.POST("", deps.Example.Create)
	examples.POST("/tasks", deps.Example.EnqueueTask)
}
