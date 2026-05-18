// Package taskqueue 给上层 service 提供一个最小化的异步任务入队接口，
// 把 *asynq.Client 隐藏在内部。service 通过包里的 Queue 类型依赖队列，
// 不直接 import asynq，便于测试 mock。
package taskqueue

import (
	"context"
	"errors"

	"github.com/hibiken/asynq"
)

var (
	// ErrQueueUnavailable 在 Queue 没拿到底层 asynq 客户端时返回（比如 Redis
	// 未配置）。service 看到这个错应该映射成 errcode.QueueUnavailable。
	ErrQueueUnavailable = errors.New("taskqueue: queue unavailable")
	// ErrNilTask 在调用方传 nil task 时返回，属于程序员 bug，不应在生产命中。
	ErrNilTask = errors.New("taskqueue: nil task")
)

// Queue 是入队 API 的薄封装，对外只暴露 Available + Enqueue。Worker 进程
// 不需要 Queue（不入队，只消费），所以 worker 的 InitWorker 也注入它的
// 入队能力主要给 task 链式投递使用。
type Queue struct {
	client *asynq.Client
}

// NewQueue 包一个 asynq 客户端；client 为 nil 时返 nil，让上层判断 Available
// 而不是 panic。
func NewQueue(client *asynq.Client) *Queue {
	if client == nil {
		return nil
	}
	return &Queue{client: client}
}

// Available 报告 Queue 是否有底层 asynq 客户端。Service 在调 Enqueue 前用
// 它快速短路并返 errcode.QueueUnavailable，避免每次都依赖 Enqueue 的错误码。
func (q *Queue) Available() bool {
	return q != nil && q.client != nil
}

// Enqueue 把 task 投到 Asynq。调用方必须传非 nil ctx——nil ctx 传给底层
// EnqueueContext 会 panic，那是 caller 的 bug，不在本层兜底。
func (q *Queue) Enqueue(ctx context.Context, t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q == nil || q.client == nil {
		return nil, ErrQueueUnavailable
	}
	if t == nil {
		return nil, ErrNilTask
	}
	return q.client.EnqueueContext(ctx, t, opts...)
}
