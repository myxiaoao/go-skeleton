package config

import (
	"strings"
	"testing"
	"time"
)

// validate 在每条规则失败时返回一个聚合 error，调用方再 errors.Join 到 Load 的 errs。
// 用表驱动覆盖每条规则，命中时 err 文案必须**包含**该规则关键词，便于定位。
func TestValidateTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantErr     bool
		wantInclude string
	}{
		{
			name:    "默认全合法",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "RequestTimeout=0 非法",
			mutate: func(c *Config) {
				c.Server.RequestTimeout = 0
			},
			wantErr:     true,
			wantInclude: "REQUEST_TIMEOUT",
		},
		{
			name: "RequestTimeout 负数非法",
			mutate: func(c *Config) {
				c.Server.RequestTimeout = -1 * time.Second
			},
			wantErr:     true,
			wantInclude: "REQUEST_TIMEOUT",
		},
		{
			name: "GracefulDrain 负数非法（0 合法）",
			mutate: func(c *Config) {
				c.Server.GracefulDrain = -1 * time.Second
			},
			wantErr:     true,
			wantInclude: "GRACEFUL_DRAIN",
		},
		{
			name: "GracefulDrain=0 合法",
			mutate: func(c *Config) {
				c.Server.GracefulDrain = 0
			},
			wantErr: false,
		},
		{
			name: "Postgres DSN 非空时 MaxOpenConns 必须正",
			mutate: func(c *Config) {
				c.Postgres.MaxOpenConns = 0
			},
			wantErr:     true,
			wantInclude: "DB_MAX_OPEN_CONNS",
		},
		{
			name: "Postgres DSN 非空时 MaxIdleConns 不能为负",
			mutate: func(c *Config) {
				c.Postgres.MaxIdleConns = -1
			},
			wantErr:     true,
			wantInclude: "DB_MAX_IDLE_CONNS",
		},
		{
			name: "Postgres DSN 为空时连接池约束跳过",
			mutate: func(c *Config) {
				c.Postgres.DSN = ""
				c.Postgres.MaxOpenConns = 0 // 本应非法，但 DSN 空时不校验
			},
			wantErr: false,
		},
		{
			name: "Worker.Queues 非空时 Concurrency 必须正",
			mutate: func(c *Config) {
				c.Worker.Concurrency = 0
			},
			wantErr:     true,
			wantInclude: "WORKER_CONCURRENCY",
		},
		{
			name: "Worker.Queues 为空时 Concurrency 约束跳过",
			mutate: func(c *Config) {
				c.Worker.Queues = nil
				c.Worker.Concurrency = 0
			},
			wantErr: false,
		},
		{
			name: "RateLimit=0 合法（不限流）",
			mutate: func(c *Config) {
				c.RateLimit.RequestsPerMinute = 0
			},
			wantErr: false,
		},
		{
			name: "RateLimit 负数非法",
			mutate: func(c *Config) {
				c.RateLimit.RequestsPerMinute = -10
			},
			wantErr:     true,
			wantInclude: "RATE_LIMIT_PER_MINUTE",
		},
		{
			name: "JWT_SECRET 非空时 JWT_ISSUER 必填",
			mutate: func(c *Config) {
				c.Auth.JWTSecret = "deadbeef"
				c.Auth.JWTIssuer = ""
			},
			wantErr:     true,
			wantInclude: "JWT_ISSUER",
		},
		{
			name: "JWT_SECRET 非空时 JWT_ISSUER 只是空白也不行",
			mutate: func(c *Config) {
				c.Auth.JWTSecret = "deadbeef"
				c.Auth.JWTIssuer = "  "
			},
			wantErr:     true,
			wantInclude: "JWT_ISSUER",
		},
		{
			name: "JWT_SECRET 为空时不校验 issuer",
			mutate: func(c *Config) {
				c.Auth.JWTSecret = ""
				c.Auth.JWTIssuer = ""
			},
			wantErr: false,
		},
		{
			name: "production 占位 JWT_SECRET 被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = "change-me-in-production"
				c.Auth.JWTIssuer = "go-skeleton"
			},
			wantErr:     true,
			wantInclude: "JWT_SECRET",
		},
		{
			name: "production 空 JWT_SECRET 被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = ""
			},
			wantErr:     true,
			wantInclude: "JWT_SECRET",
		},
		{
			name: "production 过短 JWT_SECRET 被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = "tooshort"
				c.Auth.JWTIssuer = "go-skeleton"
			},
			wantErr:     true,
			wantInclude: "at least",
		},
		{
			name: "production 足够长的真 secret 放行",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = strings.Repeat("a", minJWTSecretBytes)
				c.Auth.JWTIssuer = "go-skeleton"
			},
			wantErr: false,
		},
		{
			name: "production 开 dev token 端点被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = strings.Repeat("a", minJWTSecretBytes)
				c.Auth.JWTIssuer = "go-skeleton"
				c.Auth.DevTokenEndpointEnabled = true
			},
			wantErr:     true,
			wantInclude: "AUTH_DEV_TOKEN_ENABLED",
		},
		{
			name: "development 占位 JWT_SECRET 放行（guard 仅生产）",
			mutate: func(c *Config) {
				c.Env = EnvDevelopment
				c.Auth.JWTSecret = "change-me-in-production"
				c.Auth.JWTIssuer = "go-skeleton"
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultValidConfig()
			tc.mutate(cfg)
			err := validate(cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validate: want error, got nil")
				}
				if tc.wantInclude != "" && !strings.Contains(err.Error(), tc.wantInclude) {
					t.Errorf("validate err = %q, want contains %q", err.Error(), tc.wantInclude)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate: want nil, got %v", err)
			}
		})
	}
}

func defaultValidConfig() *Config {
	return &Config{
		Server: ServerConfig{
			RequestTimeout: 30 * time.Second,
			GracefulDrain:  10 * time.Second,
		},
		Docs: DocsConfig{
			Theme:  "system",
			Layout: "sidebar",
		},
		Postgres: PostgresConfig{
			DSN:          "postgres://x:y@localhost/db",
			MaxOpenConns: 30,
			MaxIdleConns: 15,
		},
		Worker: WorkerConfig{
			Concurrency: 10,
			Queues:      map[string]int{"default": 1},
		},
	}
}
