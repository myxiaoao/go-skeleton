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

// ExampleProcessor 示意"业务依赖怎么注入到 worker"。worker 不直接持
// *gorm.DB —— 那是 repository 的事；业务逻辑挂在 service / usecase 上，
// 通过本地接口隔离引入。当前 example 任务没有真正的业务步骤，所以接口里
// 没有方法，仅作 nil-vs-not-nil 的可用性信号。
//
// 真正的业务任务应该把 service 方法暴露成接口（如 OrderShipper.MarkShipped
// (ctx, orderID) error），然后 internal/worker.go::buildWorkerDeps 注入
// service 实例，worker handler 调接口完成业务。
type ExampleProcessor interface{}

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

// HandleExampleTask 消费 example 异步任务。当前 demo 仅打日志，真实业务
// 会改成调 Example service 接口完成持久化 / 外部调用。
func (d *Deps) HandleExampleTask(ctx context.Context, t *asynq.Task) error {
	var p task.ExamplePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal example payload: %w", err)
	}

	applog.FromContext(ctx).Info("example task executed",
		zap.String("name", p.Name),
		zap.Bool("example_processor_available", d != nil && d.Example != nil),
	)
	return nil
}

// RegisterHandlers 把所有异步任务 handler 注册到 mux 上。注册 TraceMiddleware
// 让 task 调用链自带 trace_id；deps 为 nil 兜底成空 Deps，让 mux.Handle 注
// 册路径仍然完整。
func RegisterHandlers(mux *asynq.ServeMux, deps *Deps) {
	if mux == nil {
		return
	}
	registerTraceMiddleware(mux)
	if deps == nil {
		deps = &Deps{}
	}
	mux.HandleFunc(task.TypeExampleTask, deps.HandleExampleTask)
}
