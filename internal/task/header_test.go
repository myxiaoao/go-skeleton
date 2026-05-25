package task

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
)

// TestNewHeaderFillsVersion 验证 NewHeader 总是填当前 schema 版本，trace
// 空白会被 trim 掉。Version=0 是"caller 忘了用 NewHeader"的信号，必须显
// 式区分。
func TestNewHeaderFillsVersion(t *testing.T) {
	h := NewHeader("  trace-1  ")
	if h.Version != PayloadSchemaVersion {
		t.Fatalf("Version = %d, want %d", h.Version, PayloadSchemaVersion)
	}
	if h.TraceID != "trace-1" {
		t.Fatalf("TraceID = %q, want %q (trimmed)", h.TraceID, "trace-1")
	}

	empty := NewHeader("   ")
	if empty.TraceID != "" {
		t.Fatalf("blank traceID should trim to empty, got %q", empty.TraceID)
	}
}

// TestHeaderJSONShape 验证 Header 嵌入到 payload 时 JSON 字段被提升到顶层，
// 兼容 TraceIDFromPayload / CheckHeader 按顶层字段解析。这是嵌入设计的
// 核心兼容性保证——一旦序列化形态变了，worker 端的 trace 解析会静默失效。
func TestHeaderJSONShape(t *testing.T) {
	type stub struct {
		Header
		Name string `json:"name"`
	}
	b, err := json.Marshal(stub{Header: NewHeader("t-1"), Name: "n"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	// 顶层应当含 v / trace_id / name 三个字段，不应该出现嵌套对象包裹。
	if !strings.Contains(got, `"v":1`) ||
		!strings.Contains(got, `"trace_id":"t-1"`) ||
		!strings.Contains(got, `"name":"n"`) {
		t.Fatalf("payload JSON = %s, want top-level v/trace_id/name", got)
	}
	if strings.Contains(got, `"Header"`) {
		t.Fatalf("embedded Header should not appear as nested object: %s", got)
	}
}

// TestCheckHeader 表驱动覆盖 version 校验：在区间内放行，超界返
// ErrUnsupportedPayloadVersion；version=0（caller 忘填）必须算超界，
// 而不是放过。
func TestCheckHeader(t *testing.T) {
	supported := SupportedVersions{Min: 1, Max: 2}
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"v=0（caller 漏填）被拒", 0, true},
		{"v=1 在区间内", 1, false},
		{"v=2 在区间内", 2, false},
		{"v=3 超 Max 被拒", 3, true},
		{"v=-1 异常值被拒", -1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckHeader(Header{Version: tc.version}, supported)
			if tc.wantErr {
				var verErr ErrUnsupportedPayloadVersion
				if !errors.As(err, &verErr) {
					t.Fatalf("err = %v, want ErrUnsupportedPayloadVersion", err)
				}
				if verErr.Got != tc.version {
					t.Fatalf("ErrUnsupportedPayloadVersion.Got = %d, want %d", verErr.Got, tc.version)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

// TestCurrentSupportedAcceptsCurrentVersion 防止 CurrentSupported 跟
// PayloadSchemaVersion 失同步——升 PayloadSchemaVersion 但忘改
// CurrentSupported 会让 worker 拒掉自己刚发出来的 payload。
func TestCurrentSupportedAcceptsCurrentVersion(t *testing.T) {
	if !CurrentSupported.Contains(PayloadSchemaVersion) {
		t.Fatalf("CurrentSupported %+v does not include PayloadSchemaVersion=%d",
			CurrentSupported, PayloadSchemaVersion)
	}
}

// TestBuildTaskID 验证拼接形态、空 keys、超长 ID 三档。超长 ID 不是"err
// return"，而是"可读前缀 + hash 后缀"——它是个最后兜底，正常 caller
// 不该撞上。
func TestBuildTaskID(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		keys      []string
		want      string
	}{
		{"基本拼接", "order", []string{"shipped", "ord_123"}, "order:shipped:ord_123"},
		{"无 keys 只剩 namespace", "ping", nil, "ping"},
		{"空 namespace 也允许（caller 自己负责）", "", []string{"k"}, ":k"},
		{"key 含 ':' 不 sanitize（caller 自己负责）", "ns", []string{"a:b"}, "ns:a:b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildTaskID(tc.namespace, tc.keys...); got != tc.want {
				t.Errorf("BuildTaskID = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("超长保留 MaxTaskIDLength 且带 hash 后缀", func(t *testing.T) {
		huge := "same-prefix:" + strings.Repeat("x", MaxTaskIDLength*2)
		got := BuildTaskID("ns", huge)
		if len(got) != MaxTaskIDLength {
			t.Fatalf("len = %d, want %d", len(got), MaxTaskIDLength)
		}
		if !strings.Contains(got, ":sha256:") {
			t.Fatalf("BuildTaskID should append hash suffix for long ids, got %q", got)
		}
		if !strings.HasPrefix(got, "ns:same-prefix:") {
			t.Fatalf("BuildTaskID should keep readable prefix, got %q", got)
		}
	})

	t.Run("超长不同尾部不会因截断误碰撞", func(t *testing.T) {
		prefix := strings.Repeat("x", MaxTaskIDLength*2)
		gotA := BuildTaskID("ns", prefix+"a")
		gotB := BuildTaskID("ns", prefix+"b")
		if gotA == gotB {
			t.Fatalf("long task IDs with different suffixes collided: %q", gotA)
		}
	})
}

// TestDefaultOptions 验证 Default Option 列表包含 MaxRetry 和 Timeout——
// 升级 asynq 库时这两项的 Option 类型可能变名字，单测能在 build 阶段抓到。
// 这里不深入校验值的具体内容（asynq 没暴露 Option 的 getter），只确认
// 长度和顺序，作为基本回归保险。
func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if len(opts) != 2 {
		t.Fatalf("len(opts) = %d, want 2 (MaxRetry + Timeout)", len(opts))
	}
	// 间接验证：每个 Option 都不是 nil 接口值。
	for i, opt := range opts {
		if opt == nil {
			t.Errorf("opts[%d] is nil interface", i)
		}
	}
}

// TestMarshalPayloadEmbedsTaskType 验证序列化失败时错误信息带 task type，
// 便于线上日志一眼看到是哪类任务的 payload 烂了。
func TestMarshalPayloadEmbedsTaskType(t *testing.T) {
	// json.Marshal 在遇到 chan 时返 error——构造一个不可序列化的 payload。
	_, err := MarshalPayload("example:run", make(chan int))
	if err == nil {
		t.Fatal("MarshalPayload should fail on chan, got nil")
	}
	if !strings.Contains(err.Error(), "example:run") {
		t.Errorf("err = %v, want to contain task type 'example:run'", err)
	}
}

// 编译期保险：DefaultOptions 返回值真能传给 asynq.NewTask。
var _ = asynq.NewTask("dummy", nil, DefaultOptions()...)
