package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/handler"
	"go-skeleton/internal/middleware"
	"go-skeleton/internal/repository"
	"go-skeleton/internal/router"
	"go-skeleton/internal/service"
)

var (
	errNilRegistry   = errors.New("app: nil registry")
	errNilConfig     = errors.New("app: nil config")
	errMissingDB     = errors.New("app: missing database")
	errNilHTTPServer = errors.New("app: nil http server")
	errNilWorker     = errors.New("app: nil worker")
)

// HTTPHandlers groups pre-constructed handlers used by the HTTP server.
type HTTPHandlers struct {
	Auth    *handler.AuthHandler
	Health  *handler.HealthHandler
	Example *handler.ExampleHandler
	OpenAPI *handler.OpenAPIHandler
	// API satisfies oapi.ServerInterface at compile time, guaranteeing
	// api/openapi.yaml stays aligned with the handlers we expose.
	API *handler.APIServer
}

// Server owns the HTTP transport created from application dependencies.
type Server struct {
	Engine      *gin.Engine
	HTTP        *http.Server
	Handlers    *HTTPHandlers
	rateLimiter *middleware.IPRateLimiter
}

// NewServer wires HTTP handlers, middleware, and the underlying http.Server.
func NewServer(reg *bootstrap.Registry) (*Server, error) {
	if err := validateHTTPRegistry(reg); err != nil {
		return nil, err
	}

	var rl *middleware.IPRateLimiter
	if rpm := reg.Cfg.RateLimit.RequestsPerMinute; rpm > 0 {
		rl = middleware.NewIPRateLimiterPerMinute(rpm)
	}

	handlers := newHTTPHandlers(reg)
	engine, err := newEngine(reg, handlers, rl)
	if err != nil {
		return nil, err
	}

	return &Server{
		Engine:      engine,
		HTTP:        newHTTPServer(reg.Cfg, engine),
		Handlers:    handlers,
		rateLimiter: rl,
	}, nil
}

// Run starts serving HTTP requests until Shutdown is called.
func (s *Server) Run() error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	if err := s.HTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve http server: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server and releases owned resources.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	return s.HTTP.Shutdown(ctx)
}

// Close immediately closes the HTTP server and releases owned resources.
func (s *Server) Close() error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	if err := s.HTTP.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func validateHTTPRegistry(reg *bootstrap.Registry) error {
	switch {
	case reg == nil:
		return errNilRegistry
	case reg.Cfg == nil:
		return errNilConfig
	case reg.DB == nil || reg.DB.DB() == nil:
		return errMissingDB
	default:
		return nil
	}
}

func newHTTPHandlers(reg *bootstrap.Registry) *HTTPHandlers {
	db := reg.DB.DB()
	exampleRepository := repository.NewExampleRepository(db)
	exampleService := service.NewExampleService(exampleRepository, reg.Queue)

	authH := handler.NewAuthHandler(reg.Auth, reg.Cfg.Auth.DevTokenEndpointEnabled)
	healthH := handler.NewHealthHandler(reg.DB, reg.Cache, reg.Draining)
	exampleH := handler.NewExampleHandler(exampleService)
	openapiH := handler.NewOpenAPIHandler()

	return &HTTPHandlers{
		Auth:    authH,
		Health:  healthH,
		Example: exampleH,
		OpenAPI: openapiH,
		API: &handler.APIServer{
			Auth:    authH,
			Health:  healthH,
			Example: exampleH,
			OpenAPI: openapiH,
		},
	}
}

func newEngine(reg *bootstrap.Registry, handlers *HTTPHandlers, rl *middleware.IPRateLimiter) (*gin.Engine, error) {
	engine := gin.New()
	if err := engine.SetTrustedProxies(reg.Cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}

	engine.Use(middleware.TraceLogger(reg.Cfg.Log.AuditEnabled, reg.Cfg.Log.AuditExcludes))
	engine.Use(middleware.Recovery())
	engine.Use(middleware.Timeout(reg.Cfg.Server.RequestTimeout))
	engine.Use(middleware.CORS(reg.Cfg.Cors.AllowOrigins, reg.Cfg.Cors.AllowCredentials))
	if rl != nil {
		engine.Use(rl.Middleware())
	}

	engine.GET("/livez", handlers.Health.Live)
	engine.GET("/health", handlers.Health.Health)
	engine.GET("/openapi.json", handlers.OpenAPI.Spec)
	api := engine.Group("/api/v1")

	// Always wire BearerAuth so the OpenAPI spec and runtime routes stay
	// aligned. When reg.Auth is nil the middleware returns Unauthorized,
	// matching the protected-route contract instead of 404.
	authRequired := middleware.BearerAuth(reg.Auth)
	if err := router.RegisterRoutes(api, router.Dependencies{
		Auth:         handlers.Auth,
		AuthRequired: authRequired,
		Example:      handlers.Example,
	}); err != nil {
		return nil, err
	}

	return engine, nil
}

func newHTTPServer(cfg *config.Config, engine *gin.Engine) *http.Server {
	// ReadHeaderTimeout: short hedge against slowloris-style header drips;
	// independent of the per-request body deadline below.
	// Read/WriteTimeout: a small buffer over the business RequestTimeout so
	// the middleware-side REQUEST_TIMEOUT envelope has time to flush before
	// the server itself cuts the connection.
	const slack = 5 * time.Second
	reqTimeout := cfg.Server.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = 30 * time.Second
	}
	return &http.Server{
		Addr:              cfg.Server.Port,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       reqTimeout + slack,
		WriteTimeout:      reqTimeout + slack,
		IdleTimeout:       60 * time.Second,
	}
}
