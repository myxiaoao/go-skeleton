package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"go-skeleton/internal/task"
	"go-skeleton/internal/taskqueue"
	"go-skeleton/pkg/cache"
	applog "go-skeleton/pkg/log"
)

// ExampleProcessor 是 example 异步任务的业务处理契约——给 worker handler
// 提供一个 "有名有姓" 的方法签名而不是空 interface。worker 不直接持
// *gorm.DB——那是 repository 的事；业务逻辑挂在 service / usecase 上，通过
// 本地接口隔离引入。
//
// 接入新业务任务时，按这个模板做：
//
//  1. 在 internal/task/<domain>.go 定义 payload + NewXxxTask 工厂；
//  2. 在本文件定义 XxxProcessor 接口（方法签名收 ctx + 已解码 payload）；
//  3. 在 service 包给对应 Service 加方法实现接口；
//  4. internal/worker.go::buildWorkerDeps 注入 service 实例；
//  5. RegisterHandlers 里 mux.HandleFunc 调 typed 接口。
//
// 默认 noopExampleProcessor 让模板态（service 未接入）也能跑起来，日志会
// 提示业务未接入；真实业务必须显式注入，避免静默吞任务。
type ExampleProcessor interface {
	ProcessExample(ctx context.Context, payload task.ExamplePayload) error
}

// noopExampleProcessor 是 Deps.Example 未注入时的兜底实现：保留模板的
// "任务被消费到了"信号，避免新接 worker 的开发者以为任务丢了，同时打 warn
// 提醒"该接业务了"。生产业务必须替换它，否则任务跑了但不会有实际副作用。
type noopExampleProcessor struct{}

// ProcessExample 是 noopExampleProcessor 的兜底实现：只打 warn，不返回 error
// （返 error 会触发 asynq 重试，模板态下重试无意义）。
func (noopExampleProcessor) ProcessExample(ctx context.Context, payload task.ExamplePayload) error {
	applog.FromContext(ctx).Warn("example task consumed by noop processor; wire ExampleProcessor in buildWorkerDeps",
		zap.String("name", payload.Name),
	)
	return nil
}

// Deps 收拢所有异步任务 handler 共用的依赖。
//
// 故意**不**包含 *gorm.DB：repository 是项目里唯一允许 import gorm 的层
// （见 CLAUDE.md 分层规则）。Worker handler 需要落库的话，走 service 接口
// → repository → gorm，而不是在 worker 包内直接拿 *gorm.DB。
//
// Cache / RDB / Queue 是 pkg/ 通用工具，worker import 它们不破坏分层。
type Deps struct {
	Example ExampleProcessor
	Cache   *cache.Client
	RDB     *redis.Client
	Queue   *taskqueue.Queue
}

// ProcessorRequirement 描述一个 task processor 在装配链里的状态：
// Name 用于错误信息（如 "ExampleProcessor"），Present 表示**真业务**是否注入
// （noop 兜底不算 present——production 下漏注入会被 noop 静默 ack 掉）。
type ProcessorRequirement struct {
	Name    string
	Present bool
}

// RequiredProcessors 返回每个 task 类型对应的 processor 注入状态。
// caller（如 internal/worker.go::buildWorkerDeps）在 APP_ENV=production 下
// 调它，任一 Present=false 就 fail-fast 退出。
//
// **加新 task 类型的硬约束**：在 Deps 上加新 processor 字段后，本方法必须
// 同步追加一条记录；漏加 = production 下静默 noop。这是 CLAUDE.md §异步队列
// 里"production 漏注入 fail-fast"约束的强制执行点——把"是否需要真 processor"
// 从 reg.DB == nil 这种巧合代理换成显式声明，避免"新 task 不依赖 DB 但仍需
// 真业务处理"这种场景下旧逻辑失效。
func (d *Deps) RequiredProcessors() []ProcessorRequirement {
	if d == nil {
		return nil
	}
	// noop 兜底 (noopExampleProcessor) 是 RegisterHandlers 阶段回填的；
	// 在 buildWorkerDeps 调本方法时，d.Example 仍然只在真业务注入时非 nil。
	// 用类型断言把 noop 也过滤掉，让本方法在 RegisterHandlers 之后调也能
	// 保持语义——"noop 不算 present"。
	_, exampleIsNoop := d.Example.(noopExampleProcessor)
	return []ProcessorRequirement{
		{Name: "ExampleProcessor", Present: d.Example != nil && !exampleIsNoop},
	}
}

// HandleExampleTask 消费 example 异步任务：解 payload → 调 typed
// ExampleProcessor。处理失败返回 error 让 asynq 走重试策略；解析失败也返
// error 但属于不可恢复（数据错），上层 asynq 会按 MaxRetry 控制。
//
// Deps.Example 在 RegisterHandlers 阶段已经回填了 noopExampleProcessor，
// 所以这里**不**再 nil 兜底——如果 nil 进来说明 RegisterHandlers 被绕过
// 了，让 panic / nil deref 暴露出来比静默吞任务好。
func (d *Deps) HandleExampleTask(ctx context.Context, t *asynq.Task) error {
	var p task.ExamplePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal example payload: %w", err)
	}
	// 反序列化后第一时间校验 payload schema version：超界返 error 让 asynq
	// 重试，赌后续 worker 升级会消化；不要静默吞——吞了等于真的丢消息，
	// 走 retry 至少能从 archived 队列告警里看见。
	if err := task.CheckHeader(p.Header, task.CurrentSupported); err != nil {
		applog.FromContext(ctx).Error("example task rejected: unsupported payload version",
			zap.Int("got_version", p.Version),
			zap.Error(err),
		)
		return err
	}
	if err := d.Example.ProcessExample(ctx, p); err != nil {
		applog.FromContext(ctx).Error("example task processing failed",
			zap.String("name", p.Name),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// RegisterHandlers 把所有异步任务 handler 注册到 mux 上。注册 TraceMiddleware
// 让 task 调用链自带 trace_id；deps 为 nil 兜底成空 Deps，让 mux.Handle 注
// 册路径仍然完整。
//
// Example 未注入时回填 noopExampleProcessor：避免 HandleExampleTask 走到
// nil deref，保留模板可运行性，但 noop 会打 warn 提醒接业务。
func RegisterHandlers(mux *asynq.ServeMux, deps *Deps) {
	if mux == nil {
		return
	}
	registerTraceMiddleware(mux)
	if deps == nil {
		deps = &Deps{}
	}
	if deps.Example == nil {
		deps.Example = noopExampleProcessor{}
	}
	mux.HandleFunc(task.TypeExampleTask, deps.HandleExampleTask)
}
