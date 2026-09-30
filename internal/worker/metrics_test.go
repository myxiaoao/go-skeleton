package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"go-skeleton/pkg/metrics"
)

// mockTaskObserver 捕获 ObserveTask 调用，便于断言 type / status / 耗时。
type mockTaskObserver struct {
	calls []observedTask
}

type observedTask struct {
	taskType string
	status   string
	d        time.Duration
}

func (m *mockTaskObserver) ObserveTask(taskType, status string, d time.Duration) {
	m.calls = append(m.calls, observedTask{taskType: taskType, status: status, d: d})
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
	if obs.calls[0].taskType != "ok" || obs.calls[0].status != metrics.TaskStatusSuccess {
		t.Errorf("first call = %+v, want type=ok status=success", obs.calls[0])
	}
	if obs.calls[1].taskType != "fail" || obs.calls[1].status != metrics.TaskStatusRetry {
		t.Errorf("second call = %+v, want type=fail status=retry (no retry budget in ctx)", obs.calls[1])
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

// TestMetricsMiddlewareRecordsPanicAndRepanics 验证 handler panic 时仍记一次
// 失败（无重试信息时按 retry；asynq 在 middleware 链之外 recover，不 defer 记录会漏掉最严重的失败），
// 并且 panic 继续向上抛，让 asynq 的 recover / 重试照常生效。
func TestMetricsMiddlewareRecordsPanicAndRepanics(t *testing.T) {
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

	if len(obs.calls) != 1 || obs.calls[0].taskType != "boom" || obs.calls[0].status != metrics.TaskStatusRetry {
		t.Fatalf("observe calls = %+v, want one retry for boom", obs.calls)
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
	if obs.calls[0].taskType != unknownTaskType || obs.calls[0].status != metrics.TaskStatusRetry {
		t.Errorf("unregistered call = %+v, want type=%s retry", obs.calls[0], unknownTaskType)
	}
	if obs.calls[1].taskType != "demo" {
		t.Errorf("registered call = %+v, want type=demo", obs.calls[1])
	}
}

// TestTaskStatus 覆盖 status 判定：成功 / 还有重试预算 / 预算耗尽 / SkipRetry /
// RevokeTask / 拿不到重试信息（非 asynq 上下文，保守按 retry）。
func TestTaskStatus(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		err     error
		retried int
		max     int
		ok      bool
		want    string
	}{
		{"success", nil, 5, 5, true, metrics.TaskStatusSuccess},
		{"budget left", boom, 2, 5, true, metrics.TaskStatusRetry},
		{"last attempt", boom, 5, 5, true, metrics.TaskStatusFailure},
		{"zero retry budget", boom, 0, 0, true, metrics.TaskStatusFailure},
		{"skip retry wrapped", fmt.Errorf("bad: %w", asynq.SkipRetry), 0, 5, true, metrics.TaskStatusFailure},
		{"revoke wrapped", fmt.Errorf("gone: %w", asynq.RevokeTask), 0, 5, true, metrics.TaskStatusFailure},
		{"no retry info", boom, 0, 0, false, metrics.TaskStatusRetry},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if got := taskStatus(c.err, c.retried, c.max, c.ok); got != c.want {
				t.Errorf("taskStatus = %s, want %s", got, c.want)
			}
		})
	}
}

// TestMetricsMiddlewareStatusByRetryBudget 验证 middleware 把 ctx 里的重试预算
// 接入判定：预算耗尽的错误与 panic 记 failure，否则记 retry。asynq 的 ctx 元数据
// 无法在包外构造，所以用内部构造器注入 retryInfo。
func TestMetricsMiddlewareStatusByRetryBudget(t *testing.T) {
	boom := errors.New("boom")
	final := func(context.Context) (int, int, bool) { return 3, 3, true }
	budget := func(context.Context) (int, int, bool) { return 1, 3, true }

	run := func(info retryInfoFunc, h asynq.HandlerFunc) string {
		obs := &mockTaskObserver{}
		wrapped := newMetricsMiddleware(obs, nil, info)(h)
		func() {
			defer func() { _ = recover() }()
			_ = wrapped.ProcessTask(t.Context(), asynq.NewTask("x", nil))
		}()
		if len(obs.calls) != 1 {
			t.Fatalf("observe calls = %+v, want 1", obs.calls)
		}
		return obs.calls[0].status
	}
	errH := asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return boom })
	skipH := asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return fmt.Errorf("x: %w", asynq.SkipRetry) })
	panicH := asynq.HandlerFunc(func(context.Context, *asynq.Task) error { panic("kaboom") })

	if got := run(final, errH); got != metrics.TaskStatusFailure {
		t.Errorf("exhausted budget = %s, want failure", got)
	}
	if got := run(budget, errH); got != metrics.TaskStatusRetry {
		t.Errorf("budget left = %s, want retry", got)
	}
	if got := run(budget, skipH); got != metrics.TaskStatusFailure {
		t.Errorf("SkipRetry = %s, want failure", got)
	}
	if got := run(final, panicH); got != metrics.TaskStatusFailure {
		t.Errorf("panic on last attempt = %s, want failure", got)
	}
	if got := run(budget, panicH); got != metrics.TaskStatusRetry {
		t.Errorf("panic with budget left = %s, want retry", got)
	}
}
