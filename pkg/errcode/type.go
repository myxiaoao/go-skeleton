package errcode

import "net/http"

// Error 是 service 层对外暴露的稳定错误码定义。Code 给前端做协议判断的数字，
// Reason 是机器可读的常量串（INVALID_PARAMS / DATABASE_ERROR 等）；默认人读
// 文案由 pkg/response.MessageFor 按 Reason 查表得到。字段未导出强制走构造
// 函数，避免外部代码绕过分类硬造错误码。
type Error struct {
	code   int
	reason string
}

// newError 是包内创建错误码的唯一入口；只在 common.go 里给每个错误码字面量
// 构造一次，便于集中审视代号分配。
func newError(code int, reason string) Error {
	return Error{
		code:   code,
		reason: reason,
	}
}

// Code 返回稳定的数字 API code，<=0 时回退到 0（语义上等价 success）。
func (e Error) Code() int {
	if e.code <= 0 {
		return 0
	}
	return e.code
}

// Reason 返回机器可读的常量串。零值兜底成 UNKNOWN_ERROR，避免前端拿到空串
// 无法做分支判断。
func (e Error) Reason() string {
	if e.reason == "" {
		return "UNKNOWN_ERROR"
	}
	return e.reason
}

// Error 让 errcode.Error 满足标准 error 接口。返回 Reason 而不是 Message，
// 是为了让日志和测试都用稳定的英文常量来匹配，避免文案漂移破坏断言。
func (e Error) Error() string {
	return e.Reason()
}

// HTTPStatusFor 是 HTTPStatus 的纯函数版——按 (code, reason) 映射 HTTP status。
// 供 scripts/gen-errcodes.go 这种"只有原始字段、构造不出未导出字段 Error{}"
// 的 caller 用，与 (Error).HTTPStatus() 共享同一份精确映射规则。
//
// 想加新精确映射只改 (Error).HTTPStatus 的 switch；本函数已委托给它。
func HTTPStatusFor(code int, reason string) int {
	return Error{code: code, reason: reason}.HTTPStatus()
}

// HTTPStatus 把 errcode 映射到 HTTP 状态码。pkg/response 的 WriteError /
// WriteValidationError 据此决定 c.JSON 的第一参数。
//
// 映射策略：先按 Reason 做精确覆盖（语义清晰的几条），fallback 到 code
// 段位——1xxx 客户端错误 → 400；9xxx 服务端错误 → 500；零值 / 未知 reason
// 走 500 兜底（这种情况说明上游错码构造有问题，宁可让监控亮起来而不是
// 静默 200 误导调用方）。
//
// 客户端仍以 body 里的 code 做精确判断——HTTP status 是给监控 / LB / 透明
// 代理用的粗粒度信号，body code 是给业务用的细粒度信号；两者互不替代。
func (e Error) HTTPStatus() int {
	switch e.Reason() {
	case "INVALID_PARAMS":
		return http.StatusBadRequest
	case "UNAUTHORIZED":
		return http.StatusUnauthorized
	case "PERMISSION_DENIED":
		return http.StatusForbidden
	case "TOO_MANY_REQUESTS":
		return http.StatusTooManyRequests
	case "REQUEST_TIMEOUT":
		return http.StatusRequestTimeout
	case "SERVICE_DISABLED":
		// 端点在 OpenAPI spec 里有、但被配置开关关掉了（如 dev-token 在生产
		// 环境关闭）。**不能**用 404——404 与"端点根本不存在"撞，前端无法
		// 区分。用 503 表示"服务暂时不可用"，body code=1006 / reason 进一步
		// 区分这是配置关而不是依赖挂。
		return http.StatusServiceUnavailable
	case "QUEUE_UNAVAILABLE":
		return http.StatusServiceUnavailable
	case "NOT_IMPLEMENTED_YET":
		return http.StatusNotImplemented
	}
	// code == 0 + reason 空 → 零值 Error，500 兜底（监控亮起，不静默 200）。
	// code == 0 + reason 非空（理论上不该发生）也走 500，让 caller 修复构造。
	if e.code == 0 {
		return http.StatusInternalServerError
	}
	code := e.Code()
	switch {
	case code >= 1000 && code < 2000:
		return http.StatusBadRequest
	case code >= 9000 && code < 10000:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
