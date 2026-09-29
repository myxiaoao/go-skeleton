package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// mockTaskObserver 捕获 ObserveTask 调用，便于断言 type / err / 耗时。
type mockTaskObserver struct {
	calls []observedTask
}

type observedTask struct {
	taskType string
	err      error
	d        time.Duration
}

func (m *mockTaskObserver) ObserveTask(taskType string, err error, d time.Duration) {
	m.calls = append(m.calls, observedTask{taskType: taskType, err: err, d: d})
}

// TestMetricsMiddlewareObservesSuccessAndFailure 验证 middleware 对成功 / 失败
// 各记一次，并把 handler 的 error 原样透传（不吞错，否则 asynq 不会重试）。
func TestMetricsMiddlewareObservesSuccessAndFailure(t *testing.T) {
	obs := &mockTaskObserver{}
	boom := errors.New("boom")
	h := MetricsMiddleware(obs, nil)(asynq.HandlerFunc(func(_ context.Context, t *asynq.Task) error {
		if t.Type() == "fail" {
			return boom
		}
		return nil
	}))

	if err := h.ProcessTask(t.Context(), asynq.NewTask("ok", nil)); err != nil {
		t.Fatalf("ok task returned error: %v", err)
	}
	if err := h.ProcessTask(t.Context(), asynq.NewTask("fail", nil)); !errors.Is(err, boom) {
		t.Fatalf("fail task error = %v, want %v", err, boom)
	}

	if len(obs.calls) != 2 {
		t.Fatalf("observe calls = %d, want 2", len(obs.calls))
	}
	if obs.calls[0].taskType != "ok" || obs.calls[0].err != nil {
		t.Errorf("first call = %+v, want type=ok err=nil", obs.calls[0])
	}
	if obs.calls[1].taskType != "fail" || !errors.Is(obs.calls[1].err, boom) {
		t.Errorf("second call = %+v, want type=fail err=boom", obs.calls[1])
	}
	for _, c := range obs.calls {
		if c.d < 0 {
			t.Errorf("duration should be non-negative, got %v", c.d)
		}
	}
}

// TestMetricsMiddlewareNilObserverIsPassthrough 验证 nil observer 时 middleware
// 直接透传，不 panic。
func TestMetricsMiddlewareNilObserverIsPassthrough(t *testing.T) {
	called := false
	h := MetricsMiddleware(nil, nil)(asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
		called = true
		return nil
	}))
	if err := h.ProcessTask(t.Context(), asynq.NewTask("ok", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("next handler not called")
	}
}

// TestRegisterMetricsMiddlewareOnMux 验证挂到 ServeMux 后，经 mux 分发的任务
// 会被记录；nil observer / nil mux 不 panic。
func TestRegisterMetricsMiddlewareOnMux(t *testing.T) {
	obs := &mockTaskObserver{}
	mux := asynq.NewServeMux()
	registerMetricsMiddleware(mux, obs)
	mux.HandleFunc("demo", func(context.Context, *asynq.Task) error { return nil })

	if err := mux.ProcessTask(t.Context(), asynq.NewTask("demo", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs.calls) != 1 || obs.calls[0].taskType != "demo" {
		t.Fatalf("observe calls = %+v, want one call for demo", obs.calls)
	}

	registerMetricsMiddleware(nil, obs)
	registerMetricsMiddleware(asynq.NewServeMux(), nil)
}

// TestMetricsMiddlewareCountsPanicAsFailure 验证 handler panic 时仍记一次
// failure（asynq 在 middleware 链之外 recover，不 defer 记录会漏掉最严重的失败），
// 并且 panic 继续向上抛，让 asynq 的 recover / 重试照常生效。
func TestMetricsMiddlewareCountsPanicAsFailure(t *testing.T) {
	obs := &mockTaskObserver{}
	h := MetricsMiddleware(obs, nil)(asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
		panic("kaboom")
	}))

	func() {
		defer func() {
			if r := recover(); r != "kaboom" {
				t.Fatalf("recovered = %v, want kaboom (panic must propagate)", r)
			}
		}()
		_ = h.ProcessTask(t.Context(), asynq.NewTask("boom", nil))
		t.Fatal("ProcessTask should have panicked")
	}()

	if len(obs.calls) != 1 || obs.calls[0].taskType != "boom" || obs.calls[0].err == nil {
		t.Fatalf("observe calls = %+v, want one failure for boom", obs.calls)
	}
}

// TestRegisterMetricsMiddlewareFoldsUnknownType 验证未在 mux 注册的 task type
// （走 asynq NotFoundHandler）统一记成 "unknown"，防止任意 type 撑爆 label 基数；
// 已注册的 type 用注册 pattern 作 label。
func TestRegisterMetricsMiddlewareFoldsUnknownType(t *testing.T) {
	obs := &mockTaskObserver{}
	mux := asynq.NewServeMux()
	registerMetricsMiddleware(mux, obs)
	mux.HandleFunc("demo", func(context.Context, *asynq.Task) error { return nil })

	if err := mux.ProcessTask(t.Context(), asynq.NewTask("random:type:123", nil)); err == nil {
		t.Fatal("unregistered type should return not-found error")
	}
	if err := mux.ProcessTask(t.Context(), asynq.NewTask("demo", nil)); err != nil {
		t.Fatalf("demo err = %v", err)
	}
	if len(obs.calls) != 2 {
		t.Fatalf("observe calls = %+v, want 2", obs.calls)
	}
	if obs.calls[0].taskType != unknownTaskType || obs.calls[0].err == nil {
		t.Errorf("unregistered call = %+v, want type=%s failure", obs.calls[0], unknownTaskType)
	}
	if obs.calls[1].taskType != "demo" {
		t.Errorf("registered call = %+v, want type=demo", obs.calls[1])
	}
}
