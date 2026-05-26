package app

import (
	"strings"
	"testing"

	"go-skeleton/config"
	"go-skeleton/internal/bootstrap"
)

// TestBuildWorkerDeps_ProductionFailFastWithoutProcessor 验证 production
// 下没真业务 processor（reg.DB=nil 信号）时 buildWorkerDeps 返 error，
// 避免 worker 静默以 noop 启动、消息被 ack 掉只剩 warn 日志。
func TestBuildWorkerDeps_ProductionFailFastWithoutProcessor(t *testing.T) {
	reg := &bootstrap.Registry{
		Cfg: &config.Config{Env: config.EnvProduction},
		// DB / Cache / Queue / Auth 都不注入：模拟真实漏配置场景。
	}

	deps, err := buildWorkerDeps(reg)
	if err == nil {
		t.Fatalf("expected fail-fast error in production without processor, got deps=%+v", deps)
	}
	if !strings.Contains(err.Error(), "ExampleProcessor") {
		t.Errorf("error should mention ExampleProcessor, got: %v", err)
	}
	if !strings.Contains(err.Error(), "production") {
		t.Errorf("error should mention production, got: %v", err)
	}
}

// TestBuildWorkerDeps_DevelopmentAllowsNoopFallback 验证 dev 环境保持
// 原行为：没注入 Example 也能起，RegisterHandlers 阶段回填 noop。
func TestBuildWorkerDeps_DevelopmentAllowsNoopFallback(t *testing.T) {
	reg := &bootstrap.Registry{
		Cfg: &config.Config{Env: config.EnvDevelopment},
	}

	deps, err := buildWorkerDeps(reg)
	if err != nil {
		t.Fatalf("dev should allow nil processor, got error: %v", err)
	}
	if deps == nil {
		t.Fatal("dev should return non-nil Deps even without processor")
	}
	if deps.Example != nil {
		t.Errorf("dev with reg.DB=nil should leave Example=nil for RegisterHandlers to noop-backfill, got %T", deps.Example)
	}
}
