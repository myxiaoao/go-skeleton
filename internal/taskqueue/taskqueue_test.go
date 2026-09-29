package taskqueue

import (
	"errors"
	"testing"

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

// TestQueuePingUnreachableRedisFails 用一个连不上的地址构造 client，验证
// Ping 会把底层错误透传出来（不吞、不转成 ErrQueueUnavailable，两者语义
// 不同：前者是"配置了但连不上"，后者是"压根没配置"）。
func TestQueuePingUnreachableRedisFails(t *testing.T) {
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close client: %v", err)
		}
	}()

	q := NewQueue(client)
	if err := q.Ping(t.Context()); err == nil {
		t.Fatal("expected error pinging unreachable redis")
	} else if errors.Is(err, ErrQueueUnavailable) {
		t.Fatal("unreachable redis should not be reported as ErrQueueUnavailable")
	}
}
