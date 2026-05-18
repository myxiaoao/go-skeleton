package bootstrap

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"go-skeleton/config"
	applog "go-skeleton/pkg/log"
)

// InitRuntime 设置进程级运行时：gin 模式 + 全局 zap logger。
// service 可选，传 "api" / "worker" / "migrate" 会写进 logger 的 service
// 字段，方便日志采集端按进程区分。
func InitRuntime(cfg *config.Config, service ...string) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	gin.SetMode(cfg.Server.GinMode)
	serviceName := ""
	if len(service) > 0 {
		serviceName = service[0]
	}
	if _, err := applog.Init(applog.Config{
		Level:           cfg.Log.Level,
		Format:          cfg.Log.Format,
		StacktraceLevel: cfg.Log.StacktraceLevel,
		Service:         serviceName,
	}); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	return nil
}
