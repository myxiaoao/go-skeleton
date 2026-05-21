package config

import (
	"errors"
	"fmt"
	"strings"
)

// validate 把"启动时必须自洽"的约束集中校验，避免业务运行期才发现错配。
// 规则：值合法时返 nil；不合法时返一个聚合 error，**消息里必须包含**对应的
// env var 名称（如 REQUEST_TIMEOUT），便于运维快速定位。
//
// 边界：DSN 为空 / Queues 为空时跳过相关约束——配 zero 表示该模块不启用。
func validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	var errs []error
	add := func(msg string) {
		errs = append(errs, errors.New(msg))
	}

	if cfg.Server.RequestTimeout <= 0 {
		add(fmt.Sprintf("REQUEST_TIMEOUT must be > 0, got %s", cfg.Server.RequestTimeout))
	}
	if cfg.Server.GracefulDrain < 0 {
		add(fmt.Sprintf("GRACEFUL_DRAIN must be >= 0, got %s", cfg.Server.GracefulDrain))
	}

	if strings.TrimSpace(cfg.Postgres.DSN) != "" {
		if cfg.Postgres.MaxOpenConns <= 0 {
			add(fmt.Sprintf("DB_MAX_OPEN_CONNS must be > 0, got %d", cfg.Postgres.MaxOpenConns))
		}
		if cfg.Postgres.MaxIdleConns < 0 {
			add(fmt.Sprintf("DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.Postgres.MaxIdleConns))
		}
	}

	if len(cfg.Worker.Queues) > 0 {
		if cfg.Worker.Concurrency <= 0 {
			add(fmt.Sprintf("WORKER_CONCURRENCY must be > 0 when WORKER_QUEUES set, got %d", cfg.Worker.Concurrency))
		}
	}

	if cfg.RateLimit.RequestsPerMinute < 0 {
		add(fmt.Sprintf("RATE_LIMIT_PER_MINUTE must be >= 0 (0=unlimited), got %d", cfg.RateLimit.RequestsPerMinute))
	}

	// JWT_SECRET 非空 → JWT_ISSUER 必须非空。pkg/auth.JWTManager 在 issuer 为空时
	// 会**跳过** iss claim 校验，这意味着任何持有相同 secret 但用不同 iss 颁发的
	// token 都能通过本服务验证。运维有可能 JWT_ISSUER= 显式清空覆盖 default，
	// 这层校验把这种错配拦在启动期。
	if strings.TrimSpace(cfg.Auth.JWTSecret) != "" &&
		strings.TrimSpace(cfg.Auth.JWTIssuer) == "" {
		add("JWT_ISSUER must be non-empty when JWT_SECRET is set; empty issuer disables iss claim validation")
	}

	// 生产环境安全 guard：把"复制 .env.example 上生产"这种最常见的事故拦在
	// 启动期。development 下放行，方便本地快速起服。限流 0（无限）不在这里
	// 拦——它可能是有意的（内网服务靠上游限流），只在启动日志里 warn（见 main）。
	if cfg.Env.IsProduction() {
		validateProductionSecrets(cfg, add)
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed: %w", errors.Join(errs...))
}

// insecureJWTSecrets 是绝不能用于生产的占位 secret（.env.example 默认值等）。
var insecureJWTSecrets = map[string]struct{}{
	"change-me-in-production": {},
	"change-me":               {},
	"secret":                  {},
}

// minJWTSecretBytes 是生产环境 HS256 secret 的最小长度。32 字节对应
// HMAC-SHA256 的输出宽度，低于它会显著降低暴力破解成本。
const minJWTSecretBytes = 32

// validateProductionSecrets 在 production 环境收紧 secret 相关约束：JWT_SECRET
// 必须存在、不是已知占位值、且足够长。任一不满足都 fail-fast，拒绝带着公开
// 或弱 secret 启动（攻击者能用相同 secret 伪造任意 token）。
func validateProductionSecrets(cfg *Config, add func(string)) {
	secret := strings.TrimSpace(cfg.Auth.JWTSecret)
	switch {
	case secret == "":
		add("JWT_SECRET must be set in production (APP_ENV=production)")
	case isInsecureJWTSecret(secret):
		add("JWT_SECRET is a known placeholder; set a real high-entropy secret in production (openssl rand -base64 48)")
	case len(secret) < minJWTSecretBytes:
		add(fmt.Sprintf("JWT_SECRET must be at least %d bytes in production, got %d", minJWTSecretBytes, len(secret)))
	}

	// dev-only 的 token 端点给任意 caller 颁 token，生产开着等于无鉴权后门。
	if cfg.Auth.DevTokenEndpointEnabled {
		add("AUTH_DEV_TOKEN_ENABLED must be false in production")
	}
}

func isInsecureJWTSecret(secret string) bool {
	_, ok := insecureJWTSecrets[strings.ToLower(secret)]
	return ok
}
