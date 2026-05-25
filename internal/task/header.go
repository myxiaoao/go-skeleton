package task

// Task 幂等与版本化约定：本文件集中放跨 task 的通用结构 + helper。
//
// 设计要点：
//
//   - **Header 嵌入所有 payload**：让 worker 端有一个统一入口取 trace_id /
//     校验 version，而不用按 task type 反射。Go 嵌入字段的 JSON 序列化会
//     被提升到顶层，所以 `{"v":1,"trace_id":"...","name":"foo"}` 这样的
//     payload 形态对解析端是透明的。
//
//   - **TaskID vs Unique 双 helper**：Asynq 有两套去重，语义不同，强行合
//     成一个 API 会让 caller 选错。BuildTaskID 给"业务键稳定且全局唯一"
//     的场景用（订单状态机推进），Unique window 给"防抖 / 短窗口去重"的
//     场景用（用户点按钮、定时拉取）。
//
//   - **Default* 是常量 + 工厂**：不再让每个 NewXxxTask 自己 hard-code
//     MaxRetry / Timeout。变更默认值在一处生效，业务有特殊需求时显式覆盖。

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

// PayloadSchemaVersion 是当前代码识别的 payload schema 版本。新增字段不增
// 版本号（向后兼容），改字段语义 / 删字段必须升一档。所有 NewXxxTask 工厂
// 都该填这个常量到 Header.Version。
const PayloadSchemaVersion = 1

// DefaultMaxRetry 是新建 task 推荐的 MaxRetry——5 次对应指数 backoff
// (5s, 10s, 20s, 40s, 80s) 累计 ~2 分钟，足够覆盖大部分瞬时故障；超过
// 这个次数的失败通常是业务问题，不是网络抖动，应该走 archived 死信告警
// 而不是无脑重试。业务有特殊重试需求时单独传 asynq.MaxRetry(N) 覆盖。
const DefaultMaxRetry = 5

// DefaultTimeout 是单次 task 处理的硬超时。30s 比 asynq 默认的 30min
// 严格得多——多数 task 应该秒级完成；超过 30s 通常意味着死锁 / 外部调
// 用挂死，让 asynq 切断让重试机制接手比让任务一直占着 worker 强。
// 业务有长任务（批量导出 / 大文件处理等）单独传 asynq.Timeout(t) 覆盖。
const DefaultTimeout = 30 * time.Second

// MaxTaskIDLength 是 BuildTaskID 返回值的硬上限。Asynq 在 Redis 里把
// task ID 作为 key 的一部分；过长会撑爆 Redis 内存。1KB 对正常业务键远远
// 够用，命中上限通常意味着 caller 把整段 JSON 当业务键传了。
const MaxTaskIDLength = 1024

// Header 嵌入所有 task payload 顶部，给 worker 提供 trace_id / version 的
// 统一入口。新建 payload 类型时把它**匿名嵌入**到 struct 头部：
//
//	type ExamplePayload struct {
//	    task.Header
//	    Name string `json:"name"`
//	}
//
// JSON 输出会被提升成 `{"v":1,"trace_id":"...","name":"..."}`，worker 端
// 的 TraceIDFromPayload / CheckHeader 都按顶层字段解析。
type Header struct {
	// Version 是 payload schema 版本。worker 在 CheckHeader 里校验它
	// 落在 SupportedVersions 区间内，超界拒绝任务（asynq 走 retry，等
	// 滚动升级铺完自然消化）。新建 task 必须填——零值会被当成"未填"
	// 拒收，避免老代码不知不觉绕过校验。
	Version int `json:"v"`

	// TraceID 跨服务追踪——API 端 enqueue 时把当前 ctx 的 trace_id 写
	// 进来，worker 消费时通过 TraceIDFromPayload 取回。空字符串走"无
	// trace 兜底"路径（合成 asynq:<task_id>）。
	TraceID string `json:"trace_id,omitempty"`
}

// NewHeader 构造一个填好当前 schema version 的 Header，traceID 可选（空
// 串会被序列化时省略）。NewXxxTask 工厂用它避免每次写两遍字段名。
func NewHeader(traceID string) Header {
	return Header{
		Version: PayloadSchemaVersion,
		TraceID: strings.TrimSpace(traceID),
	}
}

// SupportedVersions 描述当前 worker 接受的 payload version 闭区间 [Min, Max]。
// 滚动升级时新 worker 提前能读老 payload（Min 不变 / Max 升一档），等所
// 有 producer 切到新格式后再升 Min 把老 payload 排除掉。
type SupportedVersions struct {
	Min, Max int
}

// Contains 报告 v 是否在 [Min, Max] 闭区间内。
func (s SupportedVersions) Contains(v int) bool {
	return v >= s.Min && v <= s.Max
}

// CurrentSupported 是 worker 默认接受的 version 区间——目前只有 v1。新增
// schema 版本时（如 v2），把 Max 升到 2、暂时保留 v1 兼容老 payload；等
// 所有 producer 都升完，再把 Min 升到 2 把老消息拒掉。
var CurrentSupported = SupportedVersions{Min: PayloadSchemaVersion, Max: PayloadSchemaVersion}

// ErrUnsupportedPayloadVersion 在 CheckHeader 看到 version 超出
// SupportedVersions 区间时返回。worker handler 应该把它当成普通 error 透传，
// 让 asynq 走重试——赌后续 worker 升级会消化，比静默吞任务安全（吞了等于
// 真的丢消息，发现不了；走 retry 至少能从 archived 告警里看到）。
type ErrUnsupportedPayloadVersion struct {
	Got       int
	Supported SupportedVersions
}

func (e ErrUnsupportedPayloadVersion) Error() string {
	return fmt.Sprintf("task: payload version %d outside supported range [%d, %d]",
		e.Got, e.Supported.Min, e.Supported.Max)
}

// CheckHeader 校验 payload Header 的 version 落在 supported 区间内。worker
// handler 反序列化 payload 后第一时间调它，超界返
// ErrUnsupportedPayloadVersion 让 asynq 重试。
//
// 边界：version=0 也算"超界"——零值意味着发送端忘了填 NewHeader，让这种
// bug 在 dev 阶段就暴露而不是在生产滑过去。
func CheckHeader(h Header, supported SupportedVersions) error {
	if !supported.Contains(h.Version) {
		return ErrUnsupportedPayloadVersion{Got: h.Version, Supported: supported}
	}
	return nil
}

// DefaultOptions 返回新建 task 推荐附带的 Option 切片：DefaultMaxRetry +
// DefaultTimeout。NewXxxTask 工厂用法：
//
//	opts := append(task.DefaultOptions(), asynq.TaskID(...))
//	return asynq.NewTask(TypeXxx, payload, opts...), nil
//
// 业务有特殊需求时显式覆盖：append 后面的 Option 会覆盖前面同名的（asynq
// 内部按"最后赢"规则合并）。
func DefaultOptions() []asynq.Option {
	return []asynq.Option{
		asynq.MaxRetry(DefaultMaxRetry),
		asynq.Timeout(DefaultTimeout),
	}
}

// BuildTaskID 把 namespace + 业务键拼成稳定的 Asynq task ID，格式：
//
//	"<namespace>:<key1>:<key2>:..."
//
// 例：BuildTaskID("order", "shipped", "ord_123") → "order:shipped:ord_123"
//
// 用法（永久全局 ID 去重，同 ID 已入队会返 asynq.ErrTaskIDConflict）：
//
//	id := task.BuildTaskID("order", "shipped", orderID)
//	t, _ := task.NewOrderShippedTask(...)
//	queue.Enqueue(ctx, t, asynq.TaskID(id))
//
// 适用场景：业务键稳定且能保证全局唯一（订单状态机推进、用户操作日志去
// 重）。不适合"防抖 / 短窗口去重"——那种用 asynq.Unique(ttl) 更合适。
//
// 约定：
//   - 不做 sanitize；caller 负责保证 keys 里不含让日志难读的字符（如 ":"
//     会让 grep 时分段错位，但 Asynq 自己不要求可解析）。
//   - 空 keys 直接被拼成空段，不报错——caller 自己负责传有意义的值。
//   - 结果超过 MaxTaskIDLength 会保留可读前缀并追加 SHA-256 后缀，避免单个
//     ID 撑爆 Redis 内存，同时不让不同长 key 因简单截断而误去重。
func BuildTaskID(namespace string, keys ...string) string {
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, namespace)
	parts = append(parts, keys...)
	id := strings.Join(parts, ":")
	if len(id) > MaxTaskIDLength {
		id = truncateTaskID(id)
	}
	return id
}

func truncateTaskID(id string) string {
	sum := sha256.Sum256([]byte(id))
	suffix := fmt.Sprintf(":sha256:%x", sum)
	prefixLen := MaxTaskIDLength - len(suffix)
	if prefixLen <= 0 {
		return suffix[len(suffix)-MaxTaskIDLength:]
	}
	return id[:prefixLen] + suffix
}

// MarshalPayload 是给 NewXxxTask 工厂用的薄包装：JSON 序列化 payload，
// 失败时返回带 task type 上下文的 error，避免每个工厂都自己写一遍
// fmt.Errorf("marshal xxx payload: %w", err)。
func MarshalPayload(taskType string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("task: marshal %s payload: %w", taskType, err)
	}
	return b, nil
}
