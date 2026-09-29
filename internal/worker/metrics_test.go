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
	h := MetricsMiddleware(obs)(asynq.HandlerFunc(func(_ context.Context, t *asynq.Task) error {
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
	h := MetricsMiddleware(nil)(asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
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
