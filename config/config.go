package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Load 从环境变量读全部配置，必须在 LoadEnv 之后调。
//
// 设计点：解析错误用 errors.Join 聚合一批返出去——运维能一次性看到所有错
// 配项，不用一个个改了再 fail-fast 一次。返回值即使有错也带着部分填好的
// *Config，让上层在退出前能 log 已经成功的字段做对比。
//
// 解析全部成功后再跑一遍 validate（约束检查）；失败也包装成 error 返回。
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
		Docs: DocsConfig{
			Title:  getEnvOrDefault("DOCS_TITLE", "API Docs"),
			Theme:  getEnvOrDefault("DOCS_THEME", "system"),
			Layout: getEnvOrDefault("DOCS_LAYOUT", "sidebar"),
			Logo:   os.Getenv("DOCS_LOGO"),
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
	cfg.Env, err = environmentEnv("APP_ENV", EnvDevelopment)
	collect(err)
	cfg.Server.RequestTimeout, err = durationEnv("REQUEST_TIMEOUT", 30*time.Second)
	collect(err)
	cfg.Server.StartupProbeTimeout, err = durationEnv("STARTUP_PROBE_TIMEOUT", 5*time.Second)
	collect(err)
	cfg.Server.GracefulDrain, err = durationEnv("GRACEFUL_DRAIN", 10*time.Second)
	collect(err)
	cfg.Server.PprofEnabled, err = boolEnv("PPROF_ENABLED", false)
	collect(err)
	cfg.Server.PprofAddr = getEnvOrDefault("PPROF_ADDR", "127.0.0.1:6060")
	cfg.Server.WatchdogInterval, err = durationEnv("WATCHDOG_INTERVAL", 10*time.Second)
	collect(err)
	cfg.Server.SecurityHeadersEnabled, err = boolEnv("SECURITY_HEADERS_ENABLED", true)
	collect(err)
	cfg.Server.BodyMaxBytes, err = int64Env("BODY_MAX_BYTES", 1<<20)
	collect(err)
	cfg.Server.MetricsEnabled, err = boolEnv("METRICS_ENABLED", true)
	collect(err)

	cfg.Docs.HideTryIt, err = boolEnv("DOCS_HIDE_TRY_IT", false)
	collect(err)
	cfg.Docs.HideSchemas, err = boolEnv("DOCS_HIDE_SCHEMAS", false)
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
	cfg.Redis.PoolSize, err = intEnv("REDIS_POOL_SIZE", 0)
	collect(err)
	cfg.Redis.MinIdleConns, err = intEnv("REDIS_MIN_IDLE_CONNS", 0)
	collect(err)

	cfg.Auth.JWTTTL, err = durationEnv("JWT_TTL", 24*time.Hour)
	collect(err)
	cfg.Auth.DevTokenEndpointEnabled, err = boolEnv("AUTH_DEV_TOKEN_ENABLED", false)
	collect(err)

	cfg.Log.AuditEnabled, err = boolEnv("AUDIT_LOG_ENABLED", true)
	collect(err)

	cfg.RateLimit.RequestsPerMinute, err = intEnv("RATE_LIMIT_PER_MINUTE", 0)
	collect(err)

	cfg.Cors.AllowCredentials, err = boolEnv("CORS_ALLOW_CREDENTIALS", false)
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
	// 一致性约束（RequestTimeout > 0、连接池正数等）放在解析全部成功之后做，
	// 避免对部分填充的 cfg 报令人困惑的"二段错误"。
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// queueWeightsEnv 解析 "critical:6,default:3,low:1" 这种字符串成队列权重 map。
// 解析失败返 fallback + error，让上层走 errors.Join 兜底。
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

// environmentEnv 解析 APP_ENV，只接受 development / production（大小写不敏感）。
// 空 → fallback；非法值 → 返 fallback + error，由 errors.Join 兜底报启动失败，
// 避免把打错的环境名（如 "prod"）静默当成 development 而放过生产 guard。
func environmentEnv(key string, fallback Environment) (Environment, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	switch Environment(strings.ToLower(raw)) {
	case EnvDevelopment:
		return EnvDevelopment, nil
	case EnvProduction:
		return EnvProduction, nil
	default:
		return fallback, fmt.Errorf("%s=%q: must be one of development|production", key, raw)
	}
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

func int64Env(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
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
