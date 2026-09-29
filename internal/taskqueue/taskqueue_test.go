package taskqueue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

func TestNewQueueNil(t *testing.T) {
	if got := NewQueue(nil); got != nil {
		t.Fatalf("NewQueue(nil) = %#v, want nil", got)
	}
}

func TestQueueUnavailable(t *testing.T) {
	var q *Queue
	_, err := q.Enqueue(t.Context(), asynq.NewTask("example", nil))
	if !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("expected ErrQueueUnavailable, got %v", err)
	}
}

func TestQueueRejectsNilTask(t *testing.T) {
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	q := NewQueue(client)
	_, err := q.Enqueue(t.Context(), nil)
	if !errors.Is(err, ErrNilTask) {
		t.Fatalf("expected ErrNilTask, got %v", err)
	}
}

// TestQueuePingUnavailable 覆盖 nil Queue 和未配置底层 client 两种"不可用"
// 场景，都应该报 ErrQueueUnavailable，跟 Enqueue 的不可用语义保持一致，
// 方便 /health 的队列探针统一处理。
func TestQueuePingUnavailable(t *testing.T) {
	var nilQueue *Queue
	if err := nilQueue.Ping(t.Context()); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("nil queue Ping: expected ErrQueueUnavailable, got %v", err)
	}

	empty := NewQueue(nil)
	if err := empty.Ping(t.Context()); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("unconfigured queue Ping: expected ErrQueueUnavailable, got %v", err)
	}
}

// TestQueuePingRespectsContextTimeout 用一个已取消的 ctx 搭配连不上的地址，
// 验证 Ping 会立刻遵从 ctx 的超时/取消而返回，不会傻等底层 PING（asynq
// 自己的 Ping 不带超时，会一路等到 go-redis 的 dial/read 超时才返回，那可能
// 是好几秒）。这里刻意不构造真实网络重试场景，让测试保持快且安静。
func TestQueuePingRespectsContextTimeout(t *testing.T) {
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	q := NewQueue(client)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	err := q.Ping(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.Canceled or context.DeadlineExceeded, got %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Ping took too long to respect canceled context: %v", elapsed)
	}
}
