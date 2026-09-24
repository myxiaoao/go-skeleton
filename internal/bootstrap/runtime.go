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

// LoadConfig 是三个 cmd 入口共用的启动序列：加载 env（cmd/<service>/.env 覆盖
// 根目录 .env）→ 解析并校验配置 → InitRuntime。出错时 logger 还没就绪，调用方
// 应写 stderr 后以非零码退出——启动期配置错属于预期内的 fail-fast，不用 panic。
func LoadConfig(service string) (*config.Config, error) {
	config.LoadEnv("cmd/" + service + "/.env")
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := InitRuntime(cfg, service); err != nil {
		return nil, fmt.Errorf("init runtime: %w", err)
	}
	return cfg, nil
}
