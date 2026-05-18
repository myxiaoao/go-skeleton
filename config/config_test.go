package config

import (
	"strings"
	"testing"
	"time"
)

// envSet sets env vars for the duration of a test and restores them on cleanup.
func envSet(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadAllDefaults(t *testing.T) {
	// Wipe variables that might be set by parent process so defaults win.
	for _, k := range []string{
		"SERVER_PORT", "GIN_MODE", "REQUEST_TIMEOUT",
		"POSTGRES", "GORM_LOG_LEVEL",
		"DB_MAX_IDLE_CONNS", "DB_MAX_OPEN_CONNS", "DB_CONN_MAX_LIFETIME", "DB_CONN_MAX_IDLE_TIME",
		"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_CACHE_DB", "REDIS_QUEUE_DB",
		"JWT_SECRET", "JWT_ISSUER", "JWT_TTL", "AUTH_DEV_TOKEN_ENABLED",
		"LOG_LEVEL", "LOG_FORMAT", "LOG_STACKTRACE_LEVEL", "AUDIT_LOG_ENABLED", "AUDIT_LOG_EXCLUDE_PATHS",
		"CORS_ALLOW_ORIGINS", "RATE_LIMIT_PER_MINUTE", "TRUSTED_PROXIES",
		"WORKER_CONCURRENCY", "WORKER_QUEUES",
		"WORKER_RETRY_BASE_DELAY", "WORKER_RETRY_MAX_DELAY",
	} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with empty env returned error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Server.Port", cfg.Server.Port, ":3000"},
		{"Server.GinMode", cfg.Server.GinMode, "release"},
		{"Server.RequestTimeout", cfg.Server.RequestTimeout, 30 * time.Second},
		{"Postgres.MaxIdleConns", cfg.Postgres.MaxIdleConns, 15},
		{"Postgres.MaxOpenConns", cfg.Postgres.MaxOpenConns, 30},
		{"Postgres.ConnMaxLifetime", cfg.Postgres.ConnMaxLifetime, 30 * time.Minute},
		{"Postgres.ConnMaxIdleTime", cfg.Postgres.ConnMaxIdleTime, 5 * time.Minute},
		{"Redis.CacheDB", cfg.Redis.CacheDB, 0},
		{"Redis.QueueDB", cfg.Redis.QueueDB, 6},
		{"Auth.JWTIssuer", cfg.Auth.JWTIssuer, "go-skeleton"},
		{"Auth.JWTTTL", cfg.Auth.JWTTTL, 24 * time.Hour},
		{"Log.Level", cfg.Log.Level, "info"},
		{"Log.Format", cfg.Log.Format, "json"},
		{"Log.StacktraceLevel", cfg.Log.StacktraceLevel, "error"},
		{"Log.AuditEnabled", cfg.Log.AuditEnabled, true},
		{"RateLimit.RequestsPerMinute", cfg.RateLimit.RequestsPerMinute, 0},
		{"Auth.DevTokenEndpointEnabled", cfg.Auth.DevTokenEndpointEnabled, false},
		{"Worker.Concurrency", cfg.Worker.Concurrency, 10},
		{"Worker.RetryBaseDelay", cfg.Worker.RetryBaseDelay, 5 * time.Second},
		{"Worker.RetryMaxDelay", cfg.Worker.RetryMaxDelay, time.Hour},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}

	wantQueues := map[string]int{"critical": 6, "default": 3, "low": 1}
	if len(cfg.Worker.Queues) != len(wantQueues) {
		t.Errorf("Worker.Queues = %#v, want %#v", cfg.Worker.Queues, wantQueues)
	}
	for k, v := range wantQueues {
		if cfg.Worker.Queues[k] != v {
			t.Errorf("Worker.Queues[%s] = %d, want %d", k, cfg.Worker.Queues[k], v)
		}
	}
}

func TestLoadHonorsOverrides(t *testing.T) {
	envSet(t, map[string]string{
		"SERVER_PORT":             ":8080",
		"REQUEST_TIMEOUT":         "45s",
		"DB_MAX_OPEN_CONNS":       "100",
		"REDIS_QUEUE_DB":          "9",
		"JWT_TTL":                 "2h",
		"AUDIT_LOG_ENABLED":       "false",
		"AUDIT_LOG_EXCLUDE_PATHS": "/health, /metrics",
		"CORS_ALLOW_ORIGINS":      "https://a.com, https://b.com",
		"RATE_LIMIT_PER_MINUTE":   "120",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Server.Port != ":8080" {
		t.Errorf("Server.Port = %q, want :8080", cfg.Server.Port)
	}
	if cfg.Server.RequestTimeout != 45*time.Second {
		t.Errorf("RequestTimeout = %v, want 45s", cfg.Server.RequestTimeout)
	}
	if cfg.Postgres.MaxOpenConns != 100 {
		t.Errorf("MaxOpenConns = %d, want 100", cfg.Postgres.MaxOpenConns)
	}
	if cfg.Redis.QueueDB != 9 {
		t.Errorf("QueueDB = %d, want 9", cfg.Redis.QueueDB)
	}
	if cfg.Auth.JWTTTL != 2*time.Hour {
		t.Errorf("JWTTTL = %v, want 2h", cfg.Auth.JWTTTL)
	}
	if cfg.Log.AuditEnabled {
		t.Errorf("AuditEnabled = true, want false")
	}
	if got := cfg.Log.AuditExcludes; len(got) != 2 || got[0] != "/health" || got[1] != "/metrics" {
		t.Errorf("AuditExcludes = %#v", got)
	}
	if got := cfg.Cors.AllowOrigins; len(got) != 2 || got[0] != "https://a.com" || got[1] != "https://b.com" {
		t.Errorf("AllowOrigins = %#v", got)
	}
	if cfg.RateLimit.RequestsPerMinute != 120 {
		t.Errorf("RateLimit = %d, want 120", cfg.RateLimit.RequestsPerMinute)
	}
}

func TestLoadReturnsErrorOnGarbageInput(t *testing.T) {
	envSet(t, map[string]string{
		"DB_MAX_OPEN_CONNS": "abc",   // int parse fail
		"REQUEST_TIMEOUT":   "30",    // missing unit -> ParseDuration fail
		"AUDIT_LOG_ENABLED": "maybe", // bool parse fail
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load: expected error for garbage env values, got nil")
	}

	msg := err.Error()
	for _, want := range []string{"DB_MAX_OPEN_CONNS", "REQUEST_TIMEOUT", "AUDIT_LOG_ENABLED"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing key %s", msg, want)
		}
	}
}

func TestLoadStillReturnsConfigOnError(t *testing.T) {
	// Even with bad env, Load returns a populated *Config so the caller can
	// log/debug before fail-fast exit. Verify defaults are applied for the
	// bad keys.
	envSet(t, map[string]string{
		"DB_MAX_OPEN_CONNS": "abc",
	})

	cfg, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if cfg == nil {
		t.Fatal("expected non-nil cfg even on error")
	}
	if cfg.Postgres.MaxOpenConns != 30 {
		t.Errorf("MaxOpenConns = %d, want 30 (fallback)", cfg.Postgres.MaxOpenConns)
	}
}

func TestQueueWeightsEnvDefaults(t *testing.T) {
	t.Setenv("WORKER_QUEUES", "")
	got, err := queueWeightsEnv("WORKER_QUEUES", map[string]int{"default": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["default"] != 1 {
		t.Fatalf("got %#v, want default:1 fallback", got)
	}
}

func TestQueueWeightsEnvParses(t *testing.T) {
	t.Setenv("WORKER_QUEUES", "critical:6, default:3 , low:1")
	got, err := queueWeightsEnv("WORKER_QUEUES", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int{"critical": 6, "default": 3, "low": 1}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%s]=%d, want %d", k, got[k], v)
		}
	}
}

func TestQueueWeightsEnvRejectsGarbage(t *testing.T) {
	cases := []string{
		"critical",     // missing :weight
		"critical:abc", // non-numeric weight
		"critical:0",   // non-positive weight
		":3",           // empty name
		"critical:",    // empty weight
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("WORKER_QUEUES", raw)
			_, err := queueWeightsEnv("WORKER_QUEUES", nil)
			if err == nil {
				t.Errorf("expected error for %q, got nil", raw)
			}
		})
	}
}

func TestParseCSVHandlesWhitespaceAndEmpties(t *testing.T) {
	got := parseCSV(" a, b ,, c,  ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("parseCSV got %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("parseCSV[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
