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
