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

// TestBuildWorkerDeps_ProductionGuardListsAllMissing 验证 fail-fast 错误
// 消息列出所有缺失的 processor 名称，**通过 deps.RequiredProcessors()
// 表驱动**——确保新 task 类型加进来后只要在 RequiredProcessors 里登记，
// production guard 自动覆盖，不需要改 buildWorkerDeps。
func TestBuildWorkerDeps_ProductionGuardListsAllMissing(t *testing.T) {
	reg := &bootstrap.Registry{
		Cfg: &config.Config{Env: config.EnvProduction},
	}

	_, err := buildWorkerDeps(reg)
	if err == nil {
		t.Fatal("expected error listing missing processors")
	}
	// 当前只有 ExampleProcessor 一个 task；将来加新 task 后这一断言要扩展。
	if !strings.Contains(err.Error(), "[ExampleProcessor]") {
		t.Errorf("error should list missing processor names, got: %v", err)
	}
}
