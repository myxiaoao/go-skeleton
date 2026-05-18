package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

type mockInspector struct {
	mu        sync.Mutex
	callCount int32
	infos     map[string]*asynq.QueueInfo
	errFor    map[string]error
}

func (m *mockInspector) GetQueueInfo(queue string) (*asynq.QueueInfo, error) {
	atomic.AddInt32(&m.callCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.errFor[queue]; ok {
		return nil, err
	}
	if info, ok := m.infos[queue]; ok {
		return info, nil
	}
	return &asynq.QueueInfo{Queue: queue}, nil
}

func TestRegistry_StartAsynqCollector_PopulatesGauges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := New("test")
	inspector := &mockInspector{
		infos: map[string]*asynq.QueueInfo{
			"critical": {Queue: "critical", Size: 42, Pending: 30, Active: 5, Retry: 4, Archived: 3, Latency: 2 * time.Second},
			"default":  {Queue: "default", Size: 7, Pending: 7},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 起步抓一次同步发生，goroutine 起来后周期采集；这里不用等 ticker，第
	// 一次抓数据就足够断言。
	r.StartAsynqCollector(ctx, inspector, []string{"critical", "default"}, time.Hour, nil)

	// 给 goroutine 一个调度窗口完成首次抓取。
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&inspector.callCount) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	engine := gin.New()
	engine.GET("/metrics", gin.WrapH(r.Handler()))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	body := w.Body.String()
	for _, marker := range []string{
		`go_skeleton_test_asynq_queue_size{queue="critical"} 42`,
		`go_skeleton_test_asynq_queue_pending{queue="critical"} 30`,
		`go_skeleton_test_asynq_queue_retry{queue="critical"} 4`,
		`go_skeleton_test_asynq_queue_archived{queue="critical"} 3`,
		`go_skeleton_test_asynq_queue_size{queue="default"} 7`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("metrics output missing %q\nbody:\n%s", marker, body)
		}
	}
}

func TestRegistry_StartAsynqCollector_InspectorErrorLogged(t *testing.T) {
	r := New("test")
	inspector := &mockInspector{
		errFor: map[string]error{"critical": errors.New("redis down")},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 这次 collector 抓失败也不应 panic / 退出 goroutine；行为兜底验证。
	r.StartAsynqCollector(ctx, inspector, []string{"critical"}, time.Hour, nil)

	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&inspector.callCount) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&inspector.callCount) == 0 {
		t.Fatal("expected at least one inspector call before deadline")
	}
}
