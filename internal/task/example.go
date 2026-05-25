package task

// Example task 教学模板：task 包定义跨 API + Worker 共享的任务类型与 payload
//
//   - 任务类型常量（如 TypeExampleTask）格式建议 "<domain>:<verb>"。
//   - payload 用 struct + JSON tag，**头部匿名嵌入 task.Header**：让 worker
//     端用统一入口取 trace_id / 校验 schema version，新增字段做向后兼容
//     （加 omitempty，不删旧字段，破坏性变更升 PayloadSchemaVersion）。
//   - 创建任务的工厂函数（NewExampleTask）通过 task.DefaultOptions() 带上
//     MaxRetry / Timeout 默认值；业务有特殊需求时显式覆盖。
//   - 消费端 handler 在 internal/worker/handler.go 注册；handler 应该委托给
//     service，**不要**在 worker 里复制一份业务逻辑。

import (
	"github.com/hibiken/asynq"
)

const (
	// TypeExampleTask 是 example 异步任务的类型标识，asynq 按此 string 路由到
	// 对应 handler；格式约定 "<domain>:<verb>"，新增任务沿用这种命名。
	TypeExampleTask = "example:run"
)

// ExamplePayload 是 example 任务的 JSON payload。Header 提供 trace_id +
// schema version，Name 是业务字段。JSON 序列化后形如：
//
//	{"v":1,"trace_id":"...","name":"foo"}
//
// 嵌入字段被 Go JSON encoding 自动提升到顶层，所以 worker 端的
// TraceIDFromPayload 仍按 "trace_id" 解析；CheckHeader 按 "v" 校验。
type ExamplePayload struct {
	Header
	Name string `json:"name"`
}

// NewExampleTask 构造一个待入队的 example 任务，自动带 DefaultOptions
// （MaxRetry + Timeout）。traceID 用 variadic 是为了让无 trace 上下文的
// 调用方（脚本 / 测试）可以省略，但只取第一个；多传会被静默忽略，保持
// 调用面简单。
func NewExampleTask(name string, traceID ...string) (*asynq.Task, error) {
	var tid string
	if len(traceID) > 0 {
		tid = traceID[0]
	}
	p := ExamplePayload{
		Header: NewHeader(tid),
		Name:   name,
	}

	payload, err := MarshalPayload(TypeExampleTask, p)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeExampleTask, payload, DefaultOptions()...), nil
}
