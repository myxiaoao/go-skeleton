package config

import "time"

// Environment 标识进程运行的部署环境。它只影响"启动期安全 guard"的严格度
// （见 validate）：production 下把不安全的默认值（如示例 JWT secret）当致命
// 错误拒绝启动，development 下放行。它**不**等同于 GIN_MODE——后者是 gin
// 框架的日志/panic 行为开关，语义不同，不要互相代用。
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvProduction  Environment = "production"
)

// IsProduction 报告是否运行在生产环境。
func (e Environment) IsProduction() bool { return e == EnvProduction }

// Config 是进程启动期一次性加载的所有配置。运行时不应再修改它的字段；要
// "动态配置"的需求请走外部配置中心，不要在代码里读这里又写这里。
type Config struct {
	// Env 是部署环境标识（APP_ENV），默认 development。仅用于启动期安全 guard
	// 的严格度，不参与业务逻辑分支。
	Env       Environment
	Server    ServerConfig
	Docs      DocsConfig
	Postgres  PostgresConfig
	Redis     RedisConfig
	Auth      AuthConfig
	Cors      CorsConfig
	Log       LogConfig
	RateLimit RateLimitConfig
	Worker    WorkerConfig
}

// ServerConfig 是 HTTP server 相关的配置（监听端口、超时、安全开关等）。
type ServerConfig struct {
	Port           string
	GinMode        string
	TrustedProxies []string
	RequestTimeout time.Duration

	// StartupProbeTimeout 是 bootstrap 阶段 DB/Redis Ping 的硬超时；
	// 超时即视为依赖不可达，进程 fail-fast 退出。
	StartupProbeTimeout time.Duration
	// GracefulDrain 收到 SIGTERM 后让 /health 先返 503 的窗口，给 LB
	// 摘流时间；之后再 Shutdown HTTP server。0 = 不 drain。
	GracefulDrain time.Duration
	// PprofEnabled 决定是否启动独立的 pprof debug 端点。生产环境只在排障时打开。
	PprofEnabled bool
	// PprofAddr 是 pprof 监听地址，**只**绑 loopback 用 SSH 隧道连过去。
	PprofAddr string
	// WatchdogInterval 是 sd_notify WATCHDOG=1 心跳周期；非 Linux 平台 stub 不调用。
	WatchdogInterval time.Duration
	// SecurityHeadersEnabled 控制是否在响应里写 X-Content-Type-Options /
	// X-Frame-Options / Referrer-Policy。默认 true，纯 JSON API 没有副作用。
	SecurityHeadersEnabled bool
	// BodyMaxBytes 限制单次请求 body 体积。0 = 不限；>0 时 handler 读 body
	// 超过会被 http.MaxBytesReader 截断成 INVALID_PARAMS。
	BodyMaxBytes int64
	// MetricsEnabled 决定是否暴露 /metrics（Prometheus pull 端点）+ 挂 HTTP
	// 指标中间件。默认 true；关掉只在某些非常受限的部署形态（如不希望任何
	// 调试端点）用。生产保持默认。
	MetricsEnabled bool
	// MetricsAddr 控制 /metrics 暴露在哪：
	//   - 空字符串（默认）：/metrics 挂在业务 engine 同端口，靠网络层保护；
	//   - 非空（如 "127.0.0.1:9090"）：起独立 http.Server 监听该地址，业务
	//     engine 不再挂 /metrics。生产推荐绑 loopback 或内网地址，让指标
	//     与业务端口在 L4 上就隔离开。
	MetricsAddr string
}

// DocsConfig 是 /docs 在线文档页（Stoplight Elements）的渲染配置。这些值在
// 启动期一次性读入并预渲染进 HTML，运行时不变。它们只影响文档 UI 的展示，
// 不参与任何业务逻辑，也不进 OpenAPI 契约。
type DocsConfig struct {
	// Title 是文档页 <title>，默认 "API Docs"。
	Title string
	// Theme 控制配色：light / dark / system（跟随系统 prefers-color-scheme）。
	// 默认 system。非法值在启动期被拒。
	Theme string
	// HideTryIt 为 true 时隐藏 Elements 的 TryIt 调试面板（只读文档）。
	HideTryIt bool
	// HideSchemas 为 true 时隐藏左侧 Schemas 列表。
	HideSchemas bool
	// Layout 是 Elements 布局：sidebar（左侧导航）或 stacked（单列堆叠）。
	// 默认 sidebar。非法值在启动期被拒。
	Layout string
	// Logo 是文档页左上角 logo 的 URL；空表示不显示。
	Logo string
}

// PostgresConfig 是数据库连接配置。DSN 为空时所有 DB 相关功能均不启用，
// API 进程会 fail-fast 退出（DB 必需）；Worker 进程允许 DSN 为空（DB 可选）。
type PostgresConfig struct {
	DSN             string
	LogLevel        string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// RedisConfig 是 Redis 配置，cache 和 queue 客户端共用 Addr + Password，
// 用不同的 DB 编号（CacheDB / QueueDB）做逻辑隔离，避免 cache 数据被
// asynq 误删。
type RedisConfig struct {
	Addr     string
	Password string
	CacheDB  int
	QueueDB  int
	// PoolSize / MinIdleConns 透传给 go-redis；0 表示用库默认。给运维在
	// 高负载下不改代码也能调连接池上限。
	PoolSize     int
	MinIdleConns int
}

// AuthConfig 是 JWT 鉴权配置。JWTSecret 为空时 BearerAuth 返
// UNAUTHORIZED，受保护接口全部走不通——这是有意为之的运维兜底。
type AuthConfig struct {
	JWTSecret string
	JWTIssuer string
	JWTTTL    time.Duration

	// DevTokenEndpointEnabled 决定是否暴露 POST /api/v1/auth/token——这个端
	// 点给任意 caller 传入的 subject 颁 token，**生产必须保持 false**，仅给
	// 骨架示例流程在开发环境用。
	DevTokenEndpointEnabled bool
}

// CorsConfig 是浏览器跨域配置，只允许 AllowOrigins 白名单里的 Origin。
type CorsConfig struct {
	AllowOrigins []string
	// AllowCredentials 控制是否回写 Access-Control-Allow-Credentials: true。
	// 骨架默认无状态 JWT 走 Authorization 头，**不需要** cookie，默认 false。
	// 仅当前端需要从浏览器自动携带 cookie / session 时再打开。
	AllowCredentials bool
}

// LogConfig 是结构化日志配置。AuditEnabled 决定是否打 HTTP 审计日志；
// AuditExcludes 用于排除高频探活路径（/health、/livez），避免刷屏。
type LogConfig struct {
	Level           string
	Format          string
	StacktraceLevel string
	AuditEnabled    bool
	AuditExcludes   []string
}

// RateLimitConfig 是 per-IP 限流配置。RequestsPerMinute=0 表示不启用限流；
// >0 时 burst 默认等于 RequestsPerMinute，允许短时突发。
type RateLimitConfig struct {
	RequestsPerMinute int
}

// WorkerConfig 是 Asynq worker 调参。
type WorkerConfig struct {
	// Concurrency 是 asynq worker 并行消费任务的数量。
	Concurrency int
	// Queues 把队列名映射到权重，按 "name:weight,name:weight" 解析。
	Queues map[string]int
	// RetryBaseDelay 是指数 backoff 的种子（delay = base * 2^n）。
	RetryBaseDelay time.Duration
	// RetryMaxDelay 给指数 backoff 封顶，防止无界增长（见 computeRetryDelay）。
	RetryMaxDelay time.Duration
}
