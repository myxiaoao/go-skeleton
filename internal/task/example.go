package task

// Example task 教学模板：task 包定义跨 API + Worker 共享的任务类型与 payload
//
//   - 任务类型常量（如 TypeExampleTask）格式建议 "<domain>:<verb>"。
//   - payload 用 struct + JSON tag；新增字段做向后兼容（不要删旧字段，加 omitempty）。
//   - 创建任务的工厂函数（NewExampleTask）配 asynq.MaxRetry / asynq.Timeout 等
//     Option，避免散落在各 caller。
//   - 消费端 handler 在 internal/worker/handler.go 注册；handler 应该委托给
//     service，**不要**在 worker 里复制一份业务逻辑。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
)

const (
	// TypeExampleTask identifies the example async task.
	TypeExampleTask = "example:run"
)

// ExamplePayload is the payload for the example task.
type ExamplePayload struct {
	Name    string `json:"name"`
	TraceID string `json:"trace_id,omitempty"`
}

// NewExampleTask creates a new example task for async processing.
func NewExampleTask(name string, traceID ...string) (*asynq.Task, error) {
	p := ExamplePayload{Name: name}
	if len(traceID) > 0 {
		p.TraceID = strings.TrimSpace(traceID[0])
	}

	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal example payload: %w", err)
	}
	return asynq.NewTask(TypeExampleTask, payload, asynq.MaxRetry(5)), nil
}
