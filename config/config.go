package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Load reads all configuration from environment variables. Call after LoadEnv.
// Returns the first batch of parse errors via errors.Join; the partially
// populated *Config is still returned so callers can log it before exiting.
func Load() (*Config, error) {
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	cfg := &Config{
		Server: ServerConfig{
			Port:           getEnvOrDefault("SERVER_PORT", ":3000"),
			GinMode:        getEnvOrDefault("GIN_MODE", "release"),
			TrustedProxies: parseCSV(os.Getenv("TRUSTED_PROXIES")),
		},
		Postgres: PostgresConfig{
			DSN:      os.Getenv("POSTGRES"),
			LogLevel: os.Getenv("GORM_LOG_LEVEL"),
		},
		Redis: RedisConfig{
			Addr:     os.Getenv("REDIS_ADDR"),
			Password: os.Getenv("REDIS_PASSWORD"),
		},
		Auth: AuthConfig{
			JWTSecret: os.Getenv("JWT_SECRET"),
			JWTIssuer: getEnvOrDefault("JWT_ISSUER", "go-skeleton"),
		},
		Cors: CorsConfig{
			AllowOrigins: parseCSV(os.Getenv("CORS_ALLOW_ORIGINS")),
		},
		Log: LogConfig{
			Level:           getEnvOrDefault("LOG_LEVEL", "info"),
			Format:          getEnvOrDefault("LOG_FORMAT", "json"),
			StacktraceLevel: getEnvOrDefault("LOG_STACKTRACE_LEVEL", "error"),
			AuditExcludes:   parseCSV(os.Getenv("AUDIT_LOG_EXCLUDE_PATHS")),
		},
	}

	var err error
	cfg.Server.RequestTimeout, err = durationEnv("REQUEST_TIMEOUT", 30*time.Second)
	collect(err)

	cfg.Postgres.MaxIdleConns, err = intEnv("DB_MAX_IDLE_CONNS", 15)
	collect(err)
	cfg.Postgres.MaxOpenConns, err = intEnv("DB_MAX_OPEN_CONNS", 30)
	collect(err)
	cfg.Postgres.ConnMaxLifetime, err = durationEnv("DB_CONN_MAX_LIFETIME", 30*time.Minute)
	collect(err)
	cfg.Postgres.ConnMaxIdleTime, err = durationEnv("DB_CONN_MAX_IDLE_TIME", 5*time.Minute)
	collect(err)

	cfg.Redis.CacheDB, err = intEnv("REDIS_CACHE_DB", 0)
	collect(err)
	cfg.Redis.QueueDB, err = intEnv("REDIS_QUEUE_DB", 6)
	collect(err)

	cfg.Auth.JWTTTL, err = durationEnv("JWT_TTL", 24*time.Hour)
	collect(err)
	cfg.Auth.DevTokenEndpointEnabled, err = boolEnv("AUTH_DEV_TOKEN_ENABLED", false)
	collect(err)

	cfg.Log.AuditEnabled, err = boolEnv("AUDIT_LOG_ENABLED", true)
	collect(err)

	cfg.RateLimit.RequestsPerMinute, err = intEnv("RATE_LIMIT_PER_MINUTE", 0)
	collect(err)

	cfg.Worker.Concurrency, err = intEnv("WORKER_CONCURRENCY", 10)
	collect(err)
	cfg.Worker.Queues, err = queueWeightsEnv("WORKER_QUEUES",
		map[string]int{"critical": 6, "default": 3, "low": 1})
	collect(err)
	cfg.Worker.RetryBaseDelay, err = durationEnv("WORKER_RETRY_BASE_DELAY", 5*time.Second)
	collect(err)
	cfg.Worker.RetryMaxDelay, err = durationEnv("WORKER_RETRY_MAX_DELAY", time.Hour)
	collect(err)

	if len(errs) > 0 {
		return cfg, fmt.Errorf("config: invalid environment variables: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// queueWeightsEnv parses "critical:6,default:3,low:1" into a queue weight map.
func queueWeightsEnv(key string, fallback map[string]int) (map[string]int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	out := map[string]int{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, weightStr, ok := strings.Cut(part, ":")
		name = strings.TrimSpace(name)
		weightStr = strings.TrimSpace(weightStr)
		if !ok || name == "" || weightStr == "" {
			return fallback, fmt.Errorf("%s: expected name:weight pairs, got %q", key, part)
		}
		weight, err := strconv.Atoi(weightStr)
		if err != nil || weight <= 0 {
			return fallback, fmt.Errorf("%s: invalid weight in %q: %w", key, part, err)
		}
		out[name] = weight
	}
	if len(out) == 0 {
		return fallback, fmt.Errorf("%s: no queue entries parsed from %q", key, raw)
	}
	return out, nil
}

func getEnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func intEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q: %w", key, raw, err)
	}
	return parsed, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q: %w", key, raw, err)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q: %w", key, raw, err)
	}
	return parsed, nil
}
