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
		{
			name: "production GIN_MODE=debug 被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = strings.Repeat("a", minJWTSecretBytes)
				c.Auth.JWTIssuer = "go-skeleton"
				c.Server.GinMode = "debug"
			},
			wantErr:     true,
			wantInclude: "GIN_MODE",
		},
		{
			name: "production LOG_FORMAT=console 被拒",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.Auth.JWTSecret = strings.Repeat("a", minJWTSecretBytes)
				c.Auth.JWTIssuer = "go-skeleton"
				c.Log.Format = "console"
			},
			wantErr:     true,
			wantInclude: "LOG_FORMAT",
		},
		{
			name: "development 下 GIN_MODE=debug 放行（guard 仅生产）",
			mutate: func(c *Config) {
				c.Env = EnvDevelopment
				c.Server.GinMode = "debug"
				c.Log.Format = "console"
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
			// 默认带 production 安全值，让 production case 不被新 guard 误伤；
			// 各 case 想测某项漏配时单独 mutate 覆盖即可。
			GinMode: "release",
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
		Log: LogConfig{
			Format: "json",
		},
		Worker: WorkerConfig{
			Concurrency: 10,
			Queues:      map[string]int{"default": 1},
		},
	}
}

// TestProductionWarnings 验证非致命漏配的 warn 列表：non-prod 永远空；
// prod 下分别覆盖三项触发条件 + 一项"全配齐"的零 warn 路径。
func TestProductionWarnings(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantCount   int
		wantInclude string // 命中时 warn 列表里至少一条包含此关键词
	}{
		{
			name: "non-production 直接返 nil",
			mutate: func(c *Config) {
				c.Env = EnvDevelopment
				c.RateLimit.RequestsPerMinute = 0
				c.Server.TrustedProxies = nil
				c.Server.MetricsEnabled = true
				c.Server.MetricsAddr = ""
			},
			wantCount: 0,
		},
		{
			name: "production 全配齐：零 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsEnabled = true
				c.Server.MetricsAddr = "127.0.0.1:9090"
			},
			wantCount: 0,
		},
		{
			name: "production RATE_LIMIT=0 触发 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 0
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
			},
			wantCount:   1,
			wantInclude: "RATE_LIMIT_PER_MINUTE",
		},
		{
			name: "production TRUSTED_PROXIES 空触发 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = nil
				c.Server.MetricsAddr = "127.0.0.1:9090"
			},
			wantCount:   1,
			wantInclude: "TRUSTED_PROXIES",
		},
		{
			name: "production metrics 同端口触发 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsEnabled = true
				c.Server.MetricsAddr = ""
			},
			wantCount:   1,
			wantInclude: "METRICS_ADDR",
		},
		{
			name: "production metrics 禁用时不 warn METRICS_ADDR",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsEnabled = false
				c.Server.MetricsAddr = ""
			},
			wantCount: 0,
		},
		{
			name: "production pprof 绑 loopback IPv4 不 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
				c.Server.PprofEnabled = true
				c.Server.PprofAddr = "127.0.0.1:6060"
			},
			wantCount: 0,
		},
		{
			name: "production pprof 绑 localhost 不 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
				c.Server.PprofEnabled = true
				c.Server.PprofAddr = "localhost:6060"
			},
			wantCount: 0,
		},
		{
			name: "production pprof 绑 0.0.0.0 触发 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
				c.Server.PprofEnabled = true
				c.Server.PprofAddr = "0.0.0.0:6060"
			},
			wantCount:   1,
			wantInclude: "PPROF",
		},
		{
			name: "production pprof 绑公网 IP 触发 warn",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
				c.Server.PprofEnabled = true
				c.Server.PprofAddr = "10.0.0.5:6060"
			},
			wantCount:   1,
			wantInclude: "PPROF",
		},
		{
			name: "production pprof 关闭时 PPROF_ADDR 不查",
			mutate: func(c *Config) {
				c.Env = EnvProduction
				c.RateLimit.RequestsPerMinute = 60
				c.Server.TrustedProxies = []string{"10.0.0.0/8"}
				c.Server.MetricsAddr = "127.0.0.1:9090"
				c.Server.PprofEnabled = false
				c.Server.PprofAddr = "0.0.0.0:6060"
			},
			wantCount: 0,
		},
		{
			name: "nil 输入安全：返 nil",
			mutate: func(*Config) { /* mutate 不用，直接传 nil 走特殊路径 */
			},
			wantCount: -1, // 哨兵：表示直接测 nil
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantCount == -1 {
				if got := ProductionWarnings(nil); got != nil {
					t.Fatalf("ProductionWarnings(nil) = %v, want nil", got)
				}
				return
			}
			cfg := defaultValidConfig()
			tc.mutate(cfg)
			warns := ProductionWarnings(cfg)
			if len(warns) != tc.wantCount {
				t.Fatalf("warn count = %d (%v), want %d", len(warns), warns, tc.wantCount)
			}
			if tc.wantInclude == "" {
				return
			}
			ok := false
			for _, w := range warns {
				if strings.Contains(w, tc.wantInclude) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("no warn contains %q, got %v", tc.wantInclude, warns)
			}
		})
	}
}
