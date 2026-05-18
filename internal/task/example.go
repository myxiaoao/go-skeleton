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
	// TypeExampleTask 是 example 异步任务的类型标识，asynq 按此 string 路由到
	// 对应 handler；格式约定 "<domain>:<verb>"，新增任务沿用这种命名。
	TypeExampleTask = "example:run"
)

// ExamplePayload 是 example 任务的 JSON payload。TraceID 由 API 端填，让
// worker 消费时跟 HTTP 链路串成一条 trace。
type ExamplePayload struct {
	Name    string `json:"name"`
	TraceID string `json:"trace_id,omitempty"`
}

// NewExampleTask 构造一个待入队的 example 任务，附带 MaxRetry(5)。traceID
// 用 variadic 是为了让无 trace 上下文的调用方（脚本 / 测试）可以省略，
// 但只取第一个；多传会被静默忽略，保持调用面简单。
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
