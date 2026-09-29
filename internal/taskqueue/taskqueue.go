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

// Ping 探测 Asynq 底层 Redis 连接是否可达，供 /health 的队列探针用。
//
// asynq v0.26 的 (*asynq.Client).Ping() 不接 ctx 参数，内部固定用
// context.Background() 发一次 PING（见 internal/rdb/rdb.go），本身不带
// 超时——实际耗时完全由底层 go-redis 客户端的 dial/read 超时和重试次数
// 决定（AsynqRedisOpt 未设置这些，走 go-redis 默认值，Redis 黑洞时可能
// 阻塞 10s+）。这与 /health 的 2s ctx 超时甚至更紧的 K8s 探针超时不匹配，
// 所以这里用 goroutine + 带缓冲 channel + select 让调用方的 ctx 真正生效：
// ctx 到期/取消时立即返回 ctx.Err()，不再傻等底层 PING。
//
// 注意：ctx 到期只是让本方法提前返回，并不能取消已经发出去的那次 PING——
// 它可能仍在后台跑到 go-redis 自己的超时才收敛。用带缓冲的 result channel
// 接收这个迟到的结果，避免 goroutine 阻塞或泄漏。
func (q *Queue) Ping(ctx context.Context) error {
	if q == nil || q.client == nil {
		return ErrQueueUnavailable
	}
	// 快路径：ctx 已经结束（超时/取消）就直接返回，不必再起一个注定被丢弃
	// 结果的 goroutine 去发 PING。
	if err := ctx.Err(); err != nil {
		return err
	}

	result := make(chan error, 1)
	go func() {
		result <- q.client.Ping()
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
