package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrape 抓一次 /metrics 文本输出，便于断言指标行。
func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("metrics want 200, got %d", w.Code)
	}
	return w.Body.String()
}

// TestRegistry_ObserveTask 验证任务计数按 type + status 分组、耗时 histogram
// 按 type 累积：status 互斥为 success / retry / failure，未知 status 折叠成 failure。
func TestRegistry_ObserveTask(t *testing.T) {
	r := New("worker")
	r.ObserveTask("example:task", TaskStatusSuccess, 20*time.Millisecond)
	r.ObserveTask("example:task", TaskStatusSuccess, 30*time.Millisecond)
	r.ObserveTask("example:task", TaskStatusRetry, time.Second)
	r.ObserveTask("example:task", TaskStatusFailure, time.Second)
	r.ObserveTask("example:task", "weird-status", time.Second)

	body := scrape(t, r)
	for _, want := range []string{
		`go_skeleton_worker_asynq_tasks_processed_total{status="success",type="example:task"} 2`,
		`go_skeleton_worker_asynq_tasks_processed_total{status="retry",type="example:task"} 1`,
		`go_skeleton_worker_asynq_tasks_processed_total{status="failure",type="example:task"} 2`,
		`go_skeleton_worker_asynq_task_duration_seconds_count{type="example:task"} 5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n%s", want, body)
		}
	}
}

// TestRegistry_ObserveTaskNilSafe 验证 nil *Registry 调 ObserveTask 不 panic，
// 让未开指标的调用方不必到处判空。
func TestRegistry_ObserveTaskNilSafe(t *testing.T) {
	var r *Registry
	r.ObserveTask("example:task", TaskStatusSuccess, time.Millisecond)
}
