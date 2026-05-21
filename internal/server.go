package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/handler"
	"go-skeleton/internal/middleware"
	"go-skeleton/internal/repository"
	"go-skeleton/internal/router"
	"go-skeleton/internal/service"
	"go-skeleton/pkg/metrics"
)

var (
	errNilRegistry   = errors.New("app: nil registry")
	errNilConfig     = errors.New("app: nil config")
	errMissingDB     = errors.New("app: missing database")
	errNilHTTPServer = errors.New("app: nil http server")
	errNilWorker     = errors.New("app: nil worker")
)

// HTTPHandlers 把所有 handler 实例打包一处，方便 NewServer 一次性装配后
// 还能被路由 / 测试拿去单独使用。API 字段是 oapi 契约检查用，绑死编译期
// 保险线，让 api/openapi.yaml 与 handler 漂移时 build 直接失败。
type HTTPHandlers struct {
	Auth    *handler.AuthHandler
	Health  *handler.HealthHandler
	Example *handler.ExampleHandler
	OpenAPI *handler.OpenAPIHandler
	// API 在编译期满足 oapi.ServerInterface，保证 api/openapi.yaml 与我们
	// 暴露的 handler 始终对齐。
	API *handler.APIServer
}

// Server 持有从 Registry 装配出来的 HTTP transport 全套：gin engine、
// http.Server、handler 集合 + per-IP 限流器（如果开了的话）。
type Server struct {
	Engine             *gin.Engine
	HTTP               *http.Server
	Handlers           *HTTPHandlers
	rateLimiter        *middleware.IPRateLimiter
	stopMetricsCollect context.CancelFunc
}

// NewServer 把 handler / 中间件 / 底层 http.Server 一次性装配齐。reg 不全
// 时返 error，让上层 fail-fast。失败不会启动 ListenAndServe。
func NewServer(reg *bootstrap.Registry) (*Server, error) {
	if err := validateHTTPRegistry(reg); err != nil {
		return nil, err
	}

	var rl *middleware.IPRateLimiter
	if rpm := reg.Cfg.RateLimit.RequestsPerMinute; rpm > 0 {
		rl = middleware.NewIPRateLimiterPerMinute(rpm)
	}

	handlers := newHTTPHandlers(reg)
	engine, metricsReg, err := newEngine(reg, handlers, rl)
	if err != nil {
		return nil, err
	}

	server := &Server{
		Engine:      engine,
		HTTP:        newHTTPServer(reg.Cfg, engine),
		Handlers:    handlers,
		rateLimiter: rl,
	}

	// metrics + inspector 都就绪时起后台 collector。collector 周期抓 asynq
	// 队列状态喂 gauge；Shutdown 调 stopMetricsCollect 取消 ctx 让它退出。
	if metricsReg != nil && reg.Inspector != nil {
		ctx, cancel := context.WithCancel(context.Background())
		server.stopMetricsCollect = cancel
		queues := queueNames(reg.Cfg.Worker.Queues)
		metricsReg.StartAsynqCollector(ctx, reg.Inspector, queues, asynqCollectInterval, nil)
	}

	return server, nil
}

// asynqCollectInterval 是 asynq 队列 metrics 的采样周期。30s 与 Prometheus
// 默认 scrape 周期一致，再勤会给 Redis 添无谓负载；更稀疏会让 alert 滞后。
const asynqCollectInterval = 30 * time.Second

// queueNames 把 config 里的"队列→权重"map 翻译成稳定顺序的队列名切片。
// 排序仅为了 Prometheus label 在重启间稳定，避免 gauge 在 register 阶段
// 因 label 顺序差异打成不同 series。
func queueNames(queues map[string]int) []string {
	if len(queues) == 0 {
		return nil
	}
	names := make([]string, 0, len(queues))
	for name := range queues {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Run 开始监听并接 HTTP 请求，直到 Shutdown 被调。屏蔽
// http.ErrServerClosed——那是正常 Shutdown 的副产品。
//
// onReady 在端口绑定成功、即将开始 Serve 时回调一次（端口已抢到、马上能接
// 请求）——给 systemd sd_notify READY=1 一个精确时机：早于此发 READY 会在
// 端口绑定失败时也骗 systemd "已就绪"。onReady 为 nil 时跳过。这里不用
// ListenAndServe 而是显式 net.Listen，正是为了把"绑定成功"这个时刻暴露出来。
func (s *Server) Run(onReady func()) error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	ln, err := net.Listen("tcp", s.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.HTTP.Addr, err)
	}
	if onReady != nil {
		onReady()
	}
	if err := s.HTTP.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http server: %w", err)
	}
	return nil
}

// Shutdown 优雅停服：先停限流器后台 goroutine + metrics collector，再让
// http.Server 走 graceful drain（等 in-flight 请求完成）。ctx 控制 drain
// 超时；超时后底层会强切。
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	if s.stopMetricsCollect != nil {
		s.stopMetricsCollect()
	}
	return s.HTTP.Shutdown(ctx)
}

// Close 直接关停 HTTP server，不等 in-flight 请求——给 Shutdown 超时后的
// 强制兜底场景用，正常停服走 Shutdown。
func (s *Server) Close() error {
	if s == nil || s.HTTP == nil {
		return errNilHTTPServer
	}
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	if s.stopMetricsCollect != nil {
		s.stopMetricsCollect()
	}
	if err := s.HTTP.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// validateHTTPRegistry 校验 HTTP 装配需要的 Registry 字段都齐了。失败的细
// 分错误便于上层日志定位是哪个组件没装配。
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

// newHTTPHandlers 按"handler 声明依赖、不构造依赖"的约定，集中在这里把
// repository → service → handler 整条链 new 出来。新增模块时在这里加 4 行
// （repo / service / handler / 挂进 HTTPHandlers）。
func newHTTPHandlers(reg *bootstrap.Registry) *HTTPHandlers {
	db := reg.DB.DB()
	exampleRepository := repository.NewExampleRepository(db)
	exampleService := service.NewExampleService(exampleRepository, reg.Queue)

	authH := handler.NewAuthHandler(reg.Auth, reg.Cfg.Auth.DevTokenEndpointEnabled)
	healthH := handler.NewHealthHandler(reg.DB, reg.Cache, reg.Draining)
	exampleH := handler.NewExampleHandler(exampleService)
	openapiH := handler.NewOpenAPIHandler(reg.Cfg.Docs)

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

// newEngine 构造 gin.Engine 并按"日志 → metrics → recover → 安全头 →
// body 限制 → 超时 → CORS → 限流"的顺序挂中间件。顺序有意义：
//   - 日志最外层吃到所有请求；
//   - metrics 紧贴日志，能观测到所有进入的请求，包括被后续中间件拒掉的
//     （例如限流后 429），这样 Grafana 看到的 QPS 就是真实入站量；
//   - recover 必须在业务之前能兜住 panic；
//   - limiter 放最后避免限流前已经做了大量准备工作。
func newEngine(reg *bootstrap.Registry, handlers *HTTPHandlers, rl *middleware.IPRateLimiter) (*gin.Engine, *metrics.Registry, error) {
	engine := gin.New()
	if err := engine.SetTrustedProxies(reg.Cfg.Server.TrustedProxies); err != nil {
		return nil, nil, fmt.Errorf("set trusted proxies: %w", err)
	}

	engine.Use(middleware.TraceLogger(reg.Cfg.Log.AuditEnabled, reg.Cfg.Log.AuditExcludes))

	var metricsReg *metrics.Registry
	if reg.Cfg.Server.MetricsEnabled {
		metricsReg = metrics.New("api")
		engine.Use(metricsReg.HTTPMiddleware())
	}

	engine.Use(middleware.Recovery())
	engine.Use(middleware.SecurityHeaders(reg.Cfg.Server.SecurityHeadersEnabled))
	engine.Use(middleware.MaxBodyBytes(reg.Cfg.Server.BodyMaxBytes))
	engine.Use(middleware.Timeout(reg.Cfg.Server.RequestTimeout))
	engine.Use(middleware.CORS(reg.Cfg.Cors.AllowOrigins, reg.Cfg.Cors.AllowCredentials))
	if rl != nil {
		engine.Use(rl.Middleware())
	}

	engine.GET("/livez", handlers.Health.Live)
	engine.GET("/health", handlers.Health.Health)
	// /openapi.json（spec）+ /docs（Stoplight Elements 在线文档页，HTML，依赖
	// 外网 CDN，复用同域 /openapi.json）只在非生产环境注册：生产隐藏 API 契约
	// 与文档 UI，减少信息泄露面。production 下两条路由根本不存在，访问得到 404。
	// 不进 oapi.ServerInterface、不改 openapi.yaml。
	if !reg.Cfg.Env.IsProduction() {
		engine.GET("/openapi.json", handlers.OpenAPI.Spec)
		engine.GET("/docs", handlers.OpenAPI.Docs)
	}
	// /metrics 故意挂在 /api/v1 之外、且不走 BearerAuth：Prometheus / Grafana
	// Agent 抓数据时不该带业务身份。生产环境靠网络层（不暴露公网 + LB
	// allowlist）保护，本地开发直接 curl 即可。
	if metricsReg != nil {
		engine.GET("/metrics", gin.WrapH(metricsReg.Handler()))
	}
	api := engine.Group("/api/v1")

	// 始终挂 BearerAuth，让 OpenAPI spec 与运行时路由对齐。reg.Auth 为 nil
	// 时中间件返 UNAUTHORIZED，符合受保护路由契约；不要换成 404。
	authRequired := middleware.BearerAuth(reg.Auth)
	if err := router.RegisterRoutes(api, router.Dependencies{
		Auth:         handlers.Auth,
		AuthRequired: authRequired,
		Example:      handlers.Example,
	}); err != nil {
		return nil, nil, err
	}

	return engine, metricsReg, nil
}

// newHTTPServer 把 cfg 翻译成裸 *http.Server，单独抽出来便于改超时 / 加
// TLS / 替换 listener 等场景。
func newHTTPServer(cfg *config.Config, engine *gin.Engine) *http.Server {
	// ReadHeaderTimeout：短超时防 slowloris 风格的 header 慢喂攻击；与下面
	// 的 per-request body deadline 是两件独立的事。
	// Read/WriteTimeout：业务 RequestTimeout 之上加 slack 缓冲，让中间件
	// 端先把 REQUEST_TIMEOUT 错误信封 flush 出去，再被 http.Server 切连接。
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
		// MaxHeaderBytes 限制 HTTP header 总大小，防御恶意客户端用超大 header
		// 拖垮内存。1MB 远超正常 Cookie + Authorization 用量；body 由
		// middleware.MaxBodyBytes 单独控制。
		MaxHeaderBytes: 1 << 20,
	}
}
