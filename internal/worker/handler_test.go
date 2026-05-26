package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"go-skeleton/internal/task"
)

// mockExampleProcessor 是 ExampleProcessor 的 inline mock：捕获最近一次调用
// 的 payload，processFunc 允许注入返回的 error 路径。
type mockExampleProcessor struct {
	lastPayload task.ExamplePayload
	called      int
	processFunc func(ctx context.Context, payload task.ExamplePayload) error
}

func (m *mockExampleProcessor) ProcessExample(ctx context.Context, payload task.ExamplePayload) error {
	m.called++
	m.lastPayload = payload
	if m.processFunc != nil {
		return m.processFunc(ctx, payload)
	}
	return nil
}

func makeExampleTaskBytes(t *testing.T, p task.ExamplePayload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestHandleExampleTaskDispatchesToProcessor 验证 typed contract：handler
// 解 payload 后必须调到注入的 ExampleProcessor，且把 payload 原样传过去。
func TestHandleExampleTaskDispatchesToProcessor(t *testing.T) {
	proc := &mockExampleProcessor{}
	deps := &Deps{Example: proc}
	body := makeExampleTaskBytes(t, task.ExamplePayload{
		Header: task.NewHeader("trace-1"),
		Name:   "hello",
	})

	if err := deps.HandleExampleTask(context.Background(),
		asynq.NewTask(task.TypeExampleTask, body)); err != nil {
		t.Fatalf("HandleExampleTask: %v", err)
	}
	if proc.called != 1 {
		t.Fatalf("processor.called = %d, want 1", proc.called)
	}
	if proc.lastPayload.Name != "hello" || proc.lastPayload.TraceID != "trace-1" {
		t.Fatalf("payload = %+v, want {Name:hello TraceID:trace-1}", proc.lastPayload)
	}
}

// TestHandleExampleTaskPropagatesProcessorError 验证 processor 返 error 时
// handler 透传——让 asynq 走重试策略，不要静默吞错。
func TestHandleExampleTaskPropagatesProcessorError(t *testing.T) {
	wantErr := errors.New("downstream blew up")
	deps := &Deps{Example: &mockExampleProcessor{
		processFunc: func(context.Context, task.ExamplePayload) error { return wantErr },
	}}
	body := makeExampleTaskBytes(t, task.ExamplePayload{
		Header: task.NewHeader(""),
		Name:   "fail",
	})

	err := deps.HandleExampleTask(context.Background(),
		asynq.NewTask(task.TypeExampleTask, body))
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// TestHandleExampleTaskRejectsUnsupportedVersion 验证 payload schema 版本
// 校验：缺 Header（version=0）被拒、超界 version 被拒，processor 不会被
// 调到——避免老 worker 不知不觉跑了新格式 payload。
func TestHandleExampleTaskRejectsUnsupportedVersion(t *testing.T) {
	tests := []struct {
		name    string
		payload task.ExamplePayload
	}{
		{
			name:    "缺 Header 默认 version=0 被拒",
			payload: task.ExamplePayload{Name: "no-header"},
		},
		{
			name: "version 超出 CurrentSupported.Max 被拒",
			payload: task.ExamplePayload{
				Header: task.Header{Version: task.CurrentSupported.Max + 1},
				Name:   "future",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proc := &mockExampleProcessor{}
			deps := &Deps{Example: proc}
			body := makeExampleTaskBytes(t, tc.payload)

			err := deps.HandleExampleTask(context.Background(),
				asynq.NewTask(task.TypeExampleTask, body))
			var verErr task.ErrUnsupportedPayloadVersion
			if !errors.As(err, &verErr) {
				t.Fatalf("err = %v, want ErrUnsupportedPayloadVersion", err)
			}
			if proc.called != 0 {
				t.Fatalf("processor.called = %d, want 0 (must reject before dispatch)", proc.called)
			}
		})
	}
}

// TestHandleExampleTaskRejectsMalformedPayload 验证 JSON 解析失败时返
// error；这是 unmarshal 错（payload 数据本身坏），不该静默放过。
func TestHandleExampleTaskRejectsMalformedPayload(t *testing.T) {
	deps := &Deps{Example: &mockExampleProcessor{}}
	err := deps.HandleExampleTask(context.Background(),
		asynq.NewTask(task.TypeExampleTask, []byte("not-json")))
	if err == nil {
		t.Fatal("want unmarshal error, got nil")
	}
}

// TestRegisterHandlersFillsNoopProcessor 验证 Deps.Example 为 nil 时
// RegisterHandlers 回填 noopExampleProcessor，避免 HandleExampleTask
// 走到 nil deref。这是模板态的可运行保险。
func TestRegisterHandlersFillsNoopProcessor(t *testing.T) {
	deps := &Deps{}
	mux := asynq.NewServeMux()
	RegisterHandlers(mux, deps)

	if deps.Example == nil {
		t.Fatal("Example should have been backfilled with noopExampleProcessor")
	}
	if _, ok := deps.Example.(noopExampleProcessor); !ok {
		t.Fatalf("Example type = %T, want noopExampleProcessor", deps.Example)
	}

	// 真跑一遍兜底 processor，确保它不 panic 且不返 error（避免触发 asynq 重试）。
	body := makeExampleTaskBytes(t, task.ExamplePayload{
		Header: task.NewHeader(""),
		Name:   "noop",
	})
	if err := deps.HandleExampleTask(context.Background(),
		asynq.NewTask(task.TypeExampleTask, body)); err != nil {
		t.Fatalf("noop processor returned err = %v, want nil", err)
	}
}

// TestRequiredProcessors 验证 Deps.RequiredProcessors() 把"真业务注入"和
// "noop 兜底"区分开——buildWorkerDeps 用它在 production 下判 fail-fast。
func TestRequiredProcessors(t *testing.T) {
	tests := []struct {
		name        string
		deps        *Deps
		wantName    string
		wantPresent bool
	}{
		{
			name:        "nil Example: not present",
			deps:        &Deps{},
			wantName:    "ExampleProcessor",
			wantPresent: false,
		},
		{
			name:        "noop Example: not present (重要：noop 不算真注入)",
			deps:        &Deps{Example: noopExampleProcessor{}},
			wantName:    "ExampleProcessor",
			wantPresent: false,
		},
		{
			name:        "real Example: present",
			deps:        &Deps{Example: &exampleProcessorStub{}},
			wantName:    "ExampleProcessor",
			wantPresent: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqs := tc.deps.RequiredProcessors()
			if len(reqs) == 0 {
				t.Fatal("RequiredProcessors should not be empty")
			}
			var got *ProcessorRequirement
			for i := range reqs {
				if reqs[i].Name == tc.wantName {
					got = &reqs[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("RequiredProcessors missing %q, got: %+v", tc.wantName, reqs)
			}
			if got.Present != tc.wantPresent {
				t.Errorf("Present = %v, want %v", got.Present, tc.wantPresent)
			}
		})
	}
}

// TestRequiredProcessors_NilDeps 验证 nil 接收者安全：返 nil，不 panic。
func TestRequiredProcessors_NilDeps(t *testing.T) {
	var d *Deps
	if got := d.RequiredProcessors(); got != nil {
		t.Errorf("nil Deps should return nil, got: %+v", got)
	}
}

// exampleProcessorStub 用于测试"非 noop 的 ExampleProcessor 视为 present"。
type exampleProcessorStub struct{}

func (*exampleProcessorStub) ProcessExample(_ context.Context, _ task.ExamplePayload) error {
	return nil
}
