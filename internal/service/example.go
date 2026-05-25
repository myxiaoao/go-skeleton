package service

// Example service 教学模板：service 层承载业务规则
//
//   - 入参用 context.Context，**禁止 *gin.Context**（worker 也要复用 service）。
//   - 依赖通过 NewXxxService(...) 注入；本文件用 ExampleRepository / ExampleQueue
//     接口隔离，方便测试（参见 example_test.go 的 inline mock 写法）。
//   - 错误统一返 errcode.XxxError，配合 applog.FromContext(ctx).Error(..., zap.Error(err))
//     把底层错误写日志，不要返 fmt.Errorf 字符串。
//   - 业务流程可以跨多个 repository / queue，但**不要**直接调用 GORM 链式 API
//     （那是 repository 的活）。
//
// 加新错误码：去 pkg/errcode/common.go 加变量 + 在 pkg/response.MessageFor 加 case +
// 跑 make docs-errcodes 重新生成 docs/errcodes.md。

import (
	"context"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/model"
	"go-skeleton/internal/task"
	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
)

// ExampleRepository 是 ExampleService 持久化层的接口边界。接口就近定义在
// service 包里（不放 repository 包），这样测试时可以 inline mock，且
// repository 实现自然满足。
type ExampleRepository interface {
	Create(ctx context.Context, example *model.Example) error
	List(ctx context.Context, limit, offset int) ([]model.Example, int64, error)
}

// ExampleQueue 是 ExampleService 的异步任务队列接口边界。具体由
// internal/taskqueue.Queue 实现；service 故意不直接拿 *asynq.Client，避免
// 测试要拉 Redis。
type ExampleQueue interface {
	Available() bool
	Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ExampleService 承载 example 资源的业务规则：参数校验、调 repository
// 落库、调 queue 投递异步任务。所有公开方法的 ctx 必须从 handler 一路
// 传下来（不能 context.Background 替换）。
type ExampleService struct {
	repo  ExampleRepository
	queue ExampleQueue
}

// NewExampleService 构造 ExampleService。queue 可以为 nil 表示队列不可用——
// EnqueueTask 此时会返 errcode.QueueUnavailable，而不是 panic。原签名用
// variadic 接收可选 queue，但调用方传多个时第 2 个起会被静默忽略，造成 API
// 歧义；显式参数更清晰。
func NewExampleService(repo ExampleRepository, queue ExampleQueue) *ExampleService {
	return &ExampleService{repo: repo, queue: queue}
}

// CreateExampleReq 是创建 example 的请求体。Name 长度上限对齐 GORM 字段
// （varchar(255)），让超长输入在 binding 阶段就返 INVALID_PARAMS，不是等
// DB 抛 SQL 错。
type CreateExampleReq struct {
	Name string `json:"name" binding:"required,max=255"`
}

// Create 落一条 example。底层错误统一记日志 + 返 errcode.DatabaseError，
// 不把 GORM 内部错误字符串透给客户端（信息泄漏 + 协议不稳定）。
func (s *ExampleService) Create(ctx context.Context, req *CreateExampleReq) (*model.Example, error) {
	example := model.Example{Name: req.Name}
	if err := s.repo.Create(ctx, &example); err != nil {
		applog.FromContext(ctx).Error("failed to create example", zap.Error(err))
		return nil, errcode.DatabaseError
	}
	return &example, nil
}

// EnqueueExampleTaskReq 是投递 example 异步任务的请求体。Name 长度对齐
// example 表字段约束（varchar(255)）。
type EnqueueExampleTaskReq struct {
	Name string `json:"name" binding:"required,max=255"`
}

// EnqueueExampleTaskRes 是异步任务投递的响应：Queued=true 表示已入队，
// 任务结果由 worker 异步处理，HTTP 这条调用并不等待结果。
type EnqueueExampleTaskRes struct {
	Queued bool `json:"queued"`
}

// EnqueueTask 把 example 任务投到 Asynq 队列。队列没配置或不可用时返
// QUEUE_UNAVAILABLE；构造任务 / 入队失败返 QUEUE_ERROR。两者都带 trace_id
// 串到 worker 侧日志，便于排查"消息丢了到底是哪边"。
func (s *ExampleService) EnqueueTask(ctx context.Context, req *EnqueueExampleTaskReq) (*EnqueueExampleTaskRes, error) {
	if s.queue == nil || !s.queue.Available() {
		return nil, errcode.QueueUnavailable
	}

	t, err := task.NewExampleTask(req.Name, applog.TraceIDFrom(ctx))
	if err != nil {
		applog.FromContext(ctx).Error("failed to create example task", zap.Error(err))
		return nil, errcode.QueueError
	}
	if _, err := s.queue.Enqueue(ctx, t); err != nil {
		applog.FromContext(ctx).Error("failed to enqueue example task", zap.Error(err))
		return nil, errcode.QueueError
	}
	return &EnqueueExampleTaskRes{Queued: true}, nil
}

// ListExamplesReq 是分页列表的查询参数。limit 上限 100 防止单次返回过大；
// offset 没设上限——业务约定 offset 分页只用于小数据集，大数据集应走 cursor。
type ListExamplesReq struct {
	Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" binding:"omitempty,min=0"`
}

// ListExamplesRes 是分页列表响应。total 在默认 READ COMMITTED 下是近似值，
// 见 repository.List 的快照一致性说明。
type ListExamplesRes struct {
	Examples []model.Example `json:"examples"`
	Total    int64           `json:"total"`
}

// ProcessExample 是 example 异步任务的业务入口——满足
// internal/worker.ExampleProcessor 接口。worker 消费到 example task 后调
// 它，service 在这里把"调 repository / 通知外部系统 / 落审计"等真实业务
// 步骤串起来。
//
// 当前模板态只是落一条 example 记录 + 打日志：让接入新业务时有一个清晰的
// "改这里"靶点，而不是空函数。返回 error 会让 asynq 按 MaxRetry 重试。
func (s *ExampleService) ProcessExample(ctx context.Context, payload task.ExamplePayload) error {
	example := model.Example{Name: payload.Name}
	if err := s.repo.Create(ctx, &example); err != nil {
		applog.FromContext(ctx).Error("process example task: persist failed",
			zap.String("name", payload.Name),
			zap.Error(err),
		)
		return err
	}
	applog.FromContext(ctx).Info("example task processed",
		zap.String("name", payload.Name),
		zap.Uint64("example_id", example.ID),
	)
	return nil
}

// List 返回分页 example 列表。Limit 缺省给 20 是项目内约定，让前端不传
// 也有可用默认值；想改默认值前先看一下分页 UI 设计。
func (s *ExampleService) List(ctx context.Context, req *ListExamplesReq) (*ListExamplesRes, error) {
	if req.Limit == 0 {
		req.Limit = 20
	}
	examples, total, err := s.repo.List(ctx, req.Limit, req.Offset)
	if err != nil {
		applog.FromContext(ctx).Error("failed to list examples", zap.Error(err))
		return nil, errcode.DatabaseError
	}

	return &ListExamplesRes{Examples: examples, Total: total}, nil
}
