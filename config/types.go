package config

import "time"

// Config holds all application configuration loaded once at startup.
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

// ServerConfig holds HTTP server settings.
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
}

// PostgresConfig holds database connection settings.
type PostgresConfig struct {
	DSN             string
	LogLevel        string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// RedisConfig holds Redis settings for cache and queue clients.
type RedisConfig struct {
	Addr     string
	Password string
	CacheDB  int
	QueueDB  int
}

// AuthConfig holds JWT authentication settings.
type AuthConfig struct {
	JWTSecret string
	JWTIssuer string
	JWTTTL    time.Duration

	// DevTokenEndpointEnabled exposes POST /api/v1/auth/token, which signs a
	// token for any caller-provided subject. It MUST stay false in production:
	// it is a development convenience for the skeleton's example flow.
	DevTokenEndpointEnabled bool
}

// CorsConfig holds allowed browser origins.
type CorsConfig struct {
	AllowOrigins []string
	// AllowCredentials 控制是否回写 Access-Control-Allow-Credentials: true。
	// 骨架默认无状态 JWT 走 Authorization 头，**不需要** cookie，默认 false。
	// 仅当前端需要从浏览器自动携带 cookie / session 时再打开。
	AllowCredentials bool
}

// LogConfig holds structured logging settings.
type LogConfig struct {
	Level           string
	Format          string
	StacktraceLevel string
	AuditEnabled    bool
	AuditExcludes   []string
}

// RateLimitConfig holds per-IP rate limit settings.
type RateLimitConfig struct {
	RequestsPerMinute int
}

// WorkerConfig holds Asynq worker tuning knobs.
type WorkerConfig struct {
	// Concurrency is the number of asynq workers processing tasks in parallel.
	Concurrency int
	// Queues maps queue name to weight, parsed from "name:weight,name:weight".
	Queues map[string]int
	// RetryBaseDelay is the seed for exponential backoff (delay = base * 2^n).
	RetryBaseDelay time.Duration
	// RetryMaxDelay caps backoff so it does not grow without bound.
	RetryMaxDelay time.Duration
}
