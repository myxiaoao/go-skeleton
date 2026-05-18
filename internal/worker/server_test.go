package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

func init() {
	// 静音日志，避免测试输出被 trace middleware / retry warn 刷屏。
	applog.SetLogger(zap.NewNop())
}

// computeRetryDelay 指数 backoff + 溢出守卫。表驱动覆盖：
// - 正常区间 0..3 严格遵循 2^n * base
// - 临界 n=29 还在 int64 安全范围、n=30 触发守卫
// - 极端 n=100 / n=-1 / base 已 > max 都被守卫拉回 max
func TestComputeRetryDelay(t *testing.T) {
	const base = 5 * time.Second
	const max = 10 * time.Minute

	tests := []struct {
		name string
		n    int
		want time.Duration
	}{
		{"attempt 0 = base", 0, base},
		{"attempt 1 = 2*base", 1, 10 * time.Second},
		{"attempt 2 = 4*base", 2, 20 * time.Second},
		{"attempt 3 = 8*base", 3, 40 * time.Second},
		{"封顶到 max", 10, max},
		{"溢出守卫 n=30 拉到 max", 30, max},
		{"极端 n=100 拉到 max", 100, max},
		{"负数 n 归零返 base", -1, base},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeRetryDelay(tc.n, base, max)
			if got != tc.want {
				t.Errorf("computeRetryDelay(%d, %s, %s) = %s, want %s",
					tc.n, base, max, got, tc.want)
			}
		})
	}
}

// 溢出守卫的"关键不变量"：返回值必须 >= 0 且 <= max，**对任意 n**。
// 这才是真正想保证的：传给 asynq 的 retry delay 绝不能是负数或超大值。
func TestComputeRetryDelayInvariants(t *testing.T) {
	const base = time.Second
	const max = time.Hour

	for n := -5; n <= 100; n++ {
		d := computeRetryDelay(n, base, max)
		if d < 0 {
			t.Errorf("n=%d returned negative %s", n, d)
		}
		if d > max {
			t.Errorf("n=%d returned %s > max %s", n, d, max)
		}
	}
}

// taskLogContext 从 payload 提取 trace_id；payload 无 trace_id 时回退到
// 用 task_id 合成的 asynq trace_id，保证后续日志总有 trace 维度。
func TestTaskLogContextUsesPayloadTraceID(t *testing.T) {
	const wantTrace = "trace-from-payload"
	payload := []byte(`{"trace_id":"` + wantTrace + `","other":1}`)
	tsk := asynq.NewTask("example:noop", payload)

	ctx, fields := taskLogContext(context.Background(), tsk, taskRuntimeMetadata{TaskID: "tid"})

	if got := applog.TraceIDFrom(ctx); got != wantTrace {
		t.Errorf("trace_id in ctx = %q, want %q", got, wantTrace)
	}
	if !containsField(fields, "trace_source", "request") {
		t.Errorf("trace_source field = %v, want 'request'", fieldsToMap(fields)["trace_source"])
	}
}

// payload 无 trace_id 时，应用 asynq:<task_id> 作为合成 trace_id，
// trace_source=asynq_task。这是任务本身触发（非来自 API enqueue）的标记。
func TestTaskLogContextFallsBackToTaskID(t *testing.T) {
	tsk := asynq.NewTask("example:noop", []byte(`{}`))
	ctx, fields := taskLogContext(context.Background(), tsk, taskRuntimeMetadata{TaskID: "tid-7"})

	got := applog.TraceIDFrom(ctx)
	if got == "" {
		t.Fatal("trace_id in ctx is empty, want a synthesised one")
	}
	if !containsField(fields, "trace_source", "asynq_task") {
		t.Errorf("trace_source field = %v, want 'asynq_task'", fieldsToMap(fields)["trace_source"])
	}
}

// traceMiddleware 用 metaProvider 注入的元数据装饰 ctx + 日志，且把 next 的
// error 原样透传（不要吞掉，asynq 才能正常 retry）。
func TestTraceMiddlewareSurfacesHandlerError(t *testing.T) {
	wantErr := errors.New("handler exploded")
	// 注意参数名用 tk 避免遮蔽 *testing.T。
	handler := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		if applog.TraceIDFrom(ctx) == "" {
			t.Fatal("traceMiddleware did not set trace_id on ctx")
		}
		return wantErr
	})

	stub := func(context.Context) taskRuntimeMetadata {
		return taskRuntimeMetadata{TaskID: "tid-1", Queue: "default", RetryCount: 2, RetryCountRecorded: true}
	}
	wrapped := traceMiddleware(handler, stub)

	err := wrapped.ProcessTask(context.Background(), asynq.NewTask("t", []byte(`{}`)))
	if !errors.Is(err, wantErr) {
		t.Errorf("traceMiddleware swallowed error: got %v, want %v", err, wantErr)
	}
}

// helpers ----------------------------------------------------------------

func containsField(fields []zap.Field, key, wantStr string) bool {
	for _, f := range fields {
		if f.Key == key && f.String == wantStr {
			return true
		}
	}
	return false
}

func fieldsToMap(fields []zap.Field) map[string]any {
	m := map[string]any{}
	for _, f := range fields {
		m[f.Key] = f.String
	}
	return m
}
