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

// ExampleRepository is the persistence dependency used by ExampleService.
type ExampleRepository interface {
	Create(ctx context.Context, example *model.Example) error
	List(ctx context.Context, limit, offset int) ([]model.Example, int64, error)
}

// ExampleQueue is the async task queue boundary used by ExampleService.
type ExampleQueue interface {
	Available() bool
	Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ExampleService handles the example application flow.
type ExampleService struct {
	repo  ExampleRepository
	queue ExampleQueue
}

// NewExampleService creates an ExampleService with the given repository.
// queue 可以为 nil 表示队列不可用——EnqueueTask 此时会返 errcode.QueueUnavailable，
// 而不是 panic。原签名用 variadic 接收可选 queue，但调用方传多个时第 2 个起会
// 被静默忽略，造成 API 歧义；显式参数更清晰。
func NewExampleService(repo ExampleRepository, queue ExampleQueue) *ExampleService {
	return &ExampleService{repo: repo, queue: queue}
}

// CreateExampleReq is the request body for creating an example.
// Name max length is aligned with the GORM column (varchar(255)) so overlong
// input surfaces as INVALID_PARAMS instead of a DB-side failure.
type CreateExampleReq struct {
	Name string `json:"name" binding:"required,max=255"`
}

// Create creates a new example.
func (s *ExampleService) Create(ctx context.Context, req *CreateExampleReq) (*model.Example, error) {
	example := model.Example{Name: req.Name}
	if err := s.repo.Create(ctx, &example); err != nil {
		applog.FromContext(ctx).Error("failed to create example", zap.Error(err))
		return nil, errcode.DatabaseError
	}
	return &example, nil
}

// EnqueueExampleTaskReq is the request body for publishing an example task.
// Name shares the example table column constraint (varchar(255)).
type EnqueueExampleTaskReq struct {
	Name string `json:"name" binding:"required,max=255"`
}

// EnqueueExampleTaskRes is the response for publishing an example task.
type EnqueueExampleTaskRes struct {
	Queued bool `json:"queued"`
}

// EnqueueTask publishes an example async task.
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

// ListExamplesReq is the request query for listing examples.
type ListExamplesReq struct {
	Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" binding:"omitempty,min=0"`
}

// ListExamplesRes is the response for listing examples.
type ListExamplesRes struct {
	Examples []model.Example `json:"examples"`
	Total    int64           `json:"total"`
}

// List returns a paginated list of examples.
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
