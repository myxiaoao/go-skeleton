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

	// DevTokenEndpointEnabled exposes POST /auth/token (signs a token for any
	// caller-provided subject). Default false; only flip to true in dev.
	DevTokenEndpointEnabled bool
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
	if deps.DevTokenEndpointEnabled {
		authRoutes.POST("/token", deps.Auth.CreateToken)
	}
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
