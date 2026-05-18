package task

import "encoding/json"

// TraceIDFromPayload 从 JSON task payload 里抽 trace_id。所有 payload 类型都
// 约定带 trace_id 字段（见 ExamplePayload），让 worker 的中间件能用一个统
// 一入口取 trace 而不用按 task type 反射。解析失败返空串，让上层走"无 trace
// 兜底用 task_id"的分支。
func TraceIDFromPayload(payload []byte) string {
	var envelope struct {
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	return envelope.TraceID
}
