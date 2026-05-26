package errcode

import "testing"

func TestErrorFields(t *testing.T) {
	if InvalidParams.Code() != 1001 {
		t.Fatalf("Code() = %d, want 1001", InvalidParams.Code())
	}
	if InvalidParams.Reason() != "INVALID_PARAMS" {
		t.Fatalf("Reason() = %q, want INVALID_PARAMS", InvalidParams.Reason())
	}
	if InvalidParams.Error() != InvalidParams.Reason() {
		t.Fatalf("Error() = %q, want %q", InvalidParams.Error(), InvalidParams.Reason())
	}
}

// TestHTTPStatus_PrecisePerReason 验证每个已定义 errcode 都映射到预期 HTTP
// 状态码——这条出错说明 errcode → HTTP 的语义对齐被破坏了。
func TestHTTPStatus_PrecisePerReason(t *testing.T) {
	cases := []struct {
		err  Error
		want int
	}{
		{InvalidParams, 400},
		{Unauthorized, 401},
		{PermissionDenied, 403},
		{TooManyRequests, 429},
		{RequestTimeout, 408},
		{ServiceDisabled, 503},
		{InternalError, 500},
		{DatabaseError, 500},
		{QueueUnavailable, 503},
		{QueueError, 500},
		{NotImplementedYet, 501},
	}
	for _, tc := range cases {
		if got := tc.err.HTTPStatus(); got != tc.want {
			t.Errorf("%s.HTTPStatus() = %d, want %d", tc.err.Reason(), got, tc.want)
		}
	}
}

// TestHTTPStatus_FallbackBySegment 验证 code 段位兜底：1xxx → 400 / 9xxx → 500，
// 即便 reason 是自定义未在精确映射里出现的串。这条保证未来新增 errcode 不
// 漏映射。
func TestHTTPStatus_FallbackBySegment(t *testing.T) {
	clientCustom := newError(1099, "CUSTOM_CLIENT_ERR")
	if got := clientCustom.HTTPStatus(); got != 400 {
		t.Errorf("1xxx fallback = %d, want 400", got)
	}
	serverCustom := newError(9999, "CUSTOM_SERVER_ERR")
	if got := serverCustom.HTTPStatus(); got != 500 {
		t.Errorf("9xxx fallback = %d, want 500", got)
	}
}

// TestHTTPStatus_ZeroValue 验证 errcode.Error{} 零值兜底成 500——出现零值说明
// caller 误用 errcode（构造时漏字段），返 500 让监控亮起来比静默 200 安全。
func TestHTTPStatus_ZeroValue(t *testing.T) {
	var zero Error
	if got := zero.HTTPStatus(); got != 500 {
		t.Errorf("zero-value HTTPStatus() = %d, want 500", got)
	}
}
