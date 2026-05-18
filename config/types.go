package config

import "time"

// Config 是进程启动期一次性加载的所有配置。运行时不应再修改它的字段；要
// "动态配置"的需求请走外部配置中心，不要在代码里读这里又写这里。
type Config struct {
	Server    ServerConfig
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
