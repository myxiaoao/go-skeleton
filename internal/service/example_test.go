package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"go-skeleton/internal/model"
	"go-skeleton/internal/task"
	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
)

// taskExamplePayload 构造测试用的 example task payload，避免在每个用例里
// 写一遍 struct literal 噪音。Header 填默认值让 payload 形态贴近真实入队
// 产出（worker handler 校验 Header；service.ProcessExample 不校验，但风
// 格统一便于将来 service 真要读 Header 时不用改测试）。
func taskExamplePayload(name string) task.ExamplePayload {
	return task.ExamplePayload{
		Header: task.NewHeader(""),
		Name:   name,
	}
}

type mockExampleRepo struct {
	createFunc func(ctx context.Context, example *model.Example) error
	listFunc   func(ctx context.Context, limit, offset int) ([]model.Example, int64, error)
}

type mockExampleQueue struct {
	available   bool
	enqueueFunc func(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

func (m *mockExampleRepo) Create(ctx context.Context, example *model.Example) error {
	return m.createFunc(ctx, example)
}

func (m *mockExampleRepo) List(ctx context.Context, limit, offset int) ([]model.Example, int64, error) {
	return m.listFunc(ctx, limit, offset)
}

func (m *mockExampleQueue) Available() bool {
	return m.available
}

func (m *mockExampleQueue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return m.enqueueFunc(ctx, t, opts...)
}

func init() {
	applog.SetLogger(zap.NewNop())
}

func TestCreateSuccess(t *testing.T) {
	repo := &mockExampleRepo{
		createFunc: func(_ context.Context, example *model.Example) error {
			example.ID = 1
			return nil
		},
	}
	svc := NewExampleService(repo, nil)

	example, err := svc.Create(context.Background(), &CreateExampleReq{Name: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if example.ID != 1 {
		t.Fatalf("expected ID 1, got %d", example.ID)
	}
	if example.Name != "test" {
		t.Fatalf("expected name test, got %q", example.Name)
	}
}

func TestEnqueueTaskSuccess(t *testing.T) {
	var taskType string
	queue := &mockExampleQueue{
		available: true,
		enqueueFunc: func(_ context.Context, t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
			taskType = t.Type()
			return &asynq.TaskInfo{}, nil
		},
	}
	svc := NewExampleService(&mockExampleRepo{}, queue)

	res, err := svc.EnqueueTask(applog.WithTraceID(context.Background(), "trace-1"), &EnqueueExampleTaskReq{Name: "test"})
	if err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if !res.Queued {
		t.Fatal("expected queued response")
	}
	if taskType != "example:run" {
		t.Fatalf("expected example task type, got %q", taskType)
	}
}

func TestEnqueueTaskQueueUnavailable(t *testing.T) {
	queue := &mockExampleQueue{available: false}
	svc := NewExampleService(&mockExampleRepo{}, queue)

	_, err := svc.EnqueueTask(context.Background(), &EnqueueExampleTaskReq{Name: "test"})
	if err == nil {
		t.Fatal("expected error")
	}

	var ec errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("expected errcode.Error, got %T", err)
	}
	if ec.Code() != errcode.QueueUnavailable.Code() {
		t.Fatalf("expected queue unavailable code, got %d", ec.Code())
	}
}

// TestProcessExampleSuccess 覆盖 worker 消费链：payload → repo.Create → 成功。
// payload.Name 必须传给 repo，否则任务等于丢了。
func TestProcessExampleSuccess(t *testing.T) {
	var gotName string
	repo := &mockExampleRepo{
		createFunc: func(_ context.Context, example *model.Example) error {
			gotName = example.Name
			example.ID = 42
			return nil
		},
	}
	svc := NewExampleService(repo, nil)

	err := svc.ProcessExample(applog.WithTraceID(context.Background(), "trace-x"),
		taskExamplePayload("queued-job"))
	if err != nil {
		t.Fatalf("ProcessExample: %v", err)
	}
	if gotName != "queued-job" {
		t.Fatalf("repo got name=%q, want %q", gotName, "queued-job")
	}
}

// TestProcessExampleRepoError 验证 repo 报错时 ProcessExample 透传原 error，
// 让 asynq 走 MaxRetry。这里**不**包装成 errcode——异步路径没有客户端，
// 重试策略只看 error 是否 nil，而不是错误码。
func TestProcessExampleRepoError(t *testing.T) {
	want := errors.New("db down")
	repo := &mockExampleRepo{
		createFunc: func(context.Context, *model.Example) error { return want },
	}
	svc := NewExampleService(repo, nil)

	err := svc.ProcessExample(context.Background(), taskExamplePayload("x"))
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestCreateDatabaseError(t *testing.T) {
	repo := &mockExampleRepo{
		createFunc: func(_ context.Context, _ *model.Example) error {
			return errors.New("connection refused")
		},
	}
	svc := NewExampleService(repo, nil)

	_, err := svc.Create(context.Background(), &CreateExampleReq{Name: "test"})
	if err == nil {
		t.Fatal("expected error")
	}

	var ec errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("expected errcode.Error, got %T", err)
	}
	if ec.Code() != errcode.DatabaseError.Code() {
		t.Fatalf("expected code %d, got %d", errcode.DatabaseError.Code(), ec.Code())
	}
}

func TestListSuccess(t *testing.T) {
	examples := []model.Example{
		{ID: 1, Name: "a"},
		{ID: 2, Name: "b"},
	}
	repo := &mockExampleRepo{
		listFunc: func(_ context.Context, _, _ int) ([]model.Example, int64, error) {
			return examples, 2, nil
		},
	}
	svc := NewExampleService(repo, nil)

	res, err := svc.List(context.Background(), &ListExamplesReq{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("expected total 2, got %d", res.Total)
	}
	if len(res.Examples) != 2 {
		t.Fatalf("expected 2 examples, got %d", len(res.Examples))
	}
}

func TestListDefaultLimit(t *testing.T) {
	var capturedLimit int
	repo := &mockExampleRepo{
		listFunc: func(_ context.Context, limit, _ int) ([]model.Example, int64, error) {
			capturedLimit = limit
			return nil, 0, nil
		},
	}
	svc := NewExampleService(repo, nil)

	_, err := svc.List(context.Background(), &ListExamplesReq{Limit: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if capturedLimit != 20 {
		t.Fatalf("expected default limit 20, got %d", capturedLimit)
	}
}

func TestListDatabaseError(t *testing.T) {
	repo := &mockExampleRepo{
		listFunc: func(_ context.Context, _, _ int) ([]model.Example, int64, error) {
			return nil, 0, errors.New("timeout")
		},
	}
	svc := NewExampleService(repo, nil)

	_, err := svc.List(context.Background(), &ListExamplesReq{Limit: 10})
	if err == nil {
		t.Fatal("expected error")
	}

	var ec errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("expected errcode.Error, got %T", err)
	}
	if ec.Code() != errcode.DatabaseError.Code() {
		t.Fatalf("expected database error code, got %d", ec.Code())
	}
}
