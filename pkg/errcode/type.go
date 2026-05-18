package errcode

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
