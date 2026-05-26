package config

import (
	"errors"
	"fmt"
	"net"
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

	switch cfg.Docs.Theme {
	case "light", "dark", "system":
	default:
		add(fmt.Sprintf("DOCS_THEME must be one of light/dark/system, got %q", cfg.Docs.Theme))
	}
	switch cfg.Docs.Layout {
	case "sidebar", "stacked":
	default:
		add(fmt.Sprintf("DOCS_LAYOUT must be one of sidebar/stacked, got %q", cfg.Docs.Layout))
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
//
// 同时把"复制 .env.example 上生产"会出问题的其他运行期约束（GIN_MODE 非
// release、LOG_FORMAT 非 json）也一并 fail-fast，避免靠 README checklist 兜底。
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

	// GIN_MODE != release：debug/test 模式会打详细的请求路由表、把 panic 的完整
	// stack trace 直接吐到响应里，公网暴露会泄露内部实现细节、放大攻击面。
	// 这是 .env.example checklist 的高频漏配项，拦在启动期最稳。
	if mode := strings.TrimSpace(cfg.Server.GinMode); mode != "" && mode != "release" {
		add(fmt.Sprintf("GIN_MODE must be \"release\" in production, got %q", mode))
	}

	// LOG_FORMAT != json：日志系统（Loki / ELK / 云厂商日志）几乎全靠结构化
	// JSON 解析；console 格式上生产 = 日志无法搜索 / 无法告警。
	if format := strings.TrimSpace(cfg.Log.Format); format != "" && format != "json" {
		add(fmt.Sprintf("LOG_FORMAT must be \"json\" in production, got %q", format))
	}
}

// ProductionWarnings 返回当前 cfg 在 production 环境下"非致命但大概率漏配"
// 的提示列表。非 production 一律返 nil。
//
// 这里和 validateProductionSecrets 的硬拦边界：硬拦针对"几乎一定是配错"的
// 项（占位 secret、非 release gin mode）；warn 针对"可能有意如此、但裸暴露
// 公网时危险"的项（不限流、metrics 同端口、无 trusted proxies）。caller 拿
// 到列表后自己决定怎么打 log，validate.go 不直接依赖 logger。
func ProductionWarnings(cfg *Config) []string {
	if cfg == nil || !cfg.Env.IsProduction() {
		return nil
	}
	var warns []string

	// RATE_LIMIT_PER_MINUTE=0：可能上游 LB/WAF 兜底限流，但裸暴露公网时是漏配。
	if cfg.RateLimit.RequestsPerMinute == 0 {
		warns = append(warns, "RATE_LIMIT_PER_MINUTE=0 disables in-process rate limiting; ensure an upstream proxy enforces it")
	}

	// TRUSTED_PROXIES 空：Gin 会忽略 X-Forwarded-For / X-Real-IP，只用
	// RemoteAddr。直连无 LB 时这是合法配置；但在 LB 后面会把所有客户端都
	// 识别成 LB IP，导致限流和审计日志失真，因此只 warn 不拦。
	if len(cfg.Server.TrustedProxies) == 0 {
		warns = append(warns, "TRUSTED_PROXIES is empty; c.ClientIP() will use RemoteAddr, so deployments behind a load balancer will rate-limit and audit the proxy IP instead of the real client IP")
	}

	// METRICS_ENABLED=true 且未配独立 listener：/metrics 跟业务 API 同端口，
	// 公网暴露 = 把 Prometheus 指标也暴露了。生产推荐 METRICS_ADDR 绑 loopback。
	if cfg.Server.MetricsEnabled && strings.TrimSpace(cfg.Server.MetricsAddr) == "" {
		warns = append(warns, "/metrics is exposed on the business API port; set METRICS_ADDR to bind a separate loopback/internal listener in production")
	}

	// PPROF_ENABLED=true 且 PPROF_ADDR 不在 loopback：pprof 端点暴露 heap /
	// goroutine / CPU profile，公网可访问 = 信息泄露 + 拒绝服务向量（profile
	// 抓取本身耗 CPU）。生产 pprof 只能绑 loopback + SSH 隧道访问。
	if cfg.Server.PprofEnabled && !isLoopbackAddr(cfg.Server.PprofAddr) {
		warns = append(warns, fmt.Sprintf("PPROF_ENABLED=true with non-loopback PPROF_ADDR=%q exposes pprof to the network; bind to 127.0.0.1 / ::1 and tunnel via SSH instead", cfg.Server.PprofAddr))
	}

	return warns
}

// isLoopbackAddr 判断 listener 地址是否绑在回环。空 addr / `:port` / `0.0.0.0:port`
// 都视为非 loopback——它们会监听所有网卡，包括公网网卡。Go 标准的 net.SplitHostPort
// 能稳定取出 host 部分，配合 net.ParseIP 走 IP 的 IsLoopback 比字符串前缀匹配
// 抗变形（"localhost"、"127.0.0.2" 这种边角也认）。
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		// addr 形如 ":6060" 或空——host 段无法解析为 loopback，按非 loopback 处理。
		return false
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isInsecureJWTSecret(secret string) bool {
	_, ok := insecureJWTSecrets[strings.ToLower(secret)]
	return ok
}
