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

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed: %w", errors.Join(errs...))
}
