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
