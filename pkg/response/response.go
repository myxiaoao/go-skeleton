package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"go-skeleton/pkg/errcode"
	customvalidator "go-skeleton/pkg/validator"
)

// Response 是项目统一的 API 响应信封。Code=0 表示成功；非 0 时 Reason 是
// 机器可读的错误常量（INVALID_PARAMS 等），Message 是默认人读文案，
// Metadata 通常带 trace_id 给前端反查日志用。
//
// HTTP 状态码：成功走 200；失败按 errcode → HTTP 映射（见 pkg/errcode.Error
// .HTTPStatus()）——1xxx 客户端错误段位走 400 / 401 / 403 / 404 / 408 / 429，
// 9xxx 服务端错误段位走 500 / 501 / 503。客户端仍以 body.code 做精确判断，
// HTTP status 给监控 / LB / 透明代理用作粗粒度信号。
//
// `/livez` 与 `/health` 例外（不走信封，直接返 200 / 503 给 K8s 探针）。
type Response struct {
	Code     int            `json:"code"`
	Message  string         `json:"message"`
	Reason   string         `json:"reason,omitempty"`
	Data     any            `json:"data,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SuccessResponse 构造 Code=0 的成功响应；只是个 helper，真正写出去走
// WriteSuccess。
func SuccessResponse(data any) Response {
	return Response{Code: 0, Message: "success", Data: data}
}

// ErrorResponse 构造业务错误信封。Code / Reason / Message 三个字段绑死
// errcode 表，避免散在各处拼字符串。
func ErrorResponse(c *gin.Context, errorCode errcode.Error) Response {
	reason := errorCode.Reason()
	return Response{
		Code:     errorCode.Code(),
		Reason:   reason,
		Message:  MessageFor(reason),
		Metadata: buildMetadata(c),
	}
}

// BuildValidationErrorResponse 把 binding 校验错误包装成 INVALID_PARAMS
// 信封。validator.ValidationErrors 走 customvalidator 翻译字段名 / 规则；
// 其他 error 直接透传 err.Error() 兜底。
func BuildValidationErrorResponse(c *gin.Context, err error) Response {
	msg := err.Error()
	if errs, ok := err.(validator.ValidationErrors); ok {
		msg = customvalidator.HandleValidatorError(errs)
	}
	return Response{
		Code:     errcode.InvalidParams.Code(),
		Reason:   errcode.InvalidParams.Reason(),
		Message:  msg,
		Metadata: buildMetadata(c),
	}
}

// MetricsCodeKey 是 gin.Context 里业务 code 的 key，pkg/metrics 的
// HTTPMiddleware 用它从 context 读 code 写到 Prometheus label。
// 暴露成常量是为了避免 metrics 包反向 import response 包形成循环依赖
// （response → errcode → 无依赖；metrics → 无依赖；约束是 metrics 不 import
// response，所以两边共享这个 key 字符串）。
const MetricsCodeKey = "response_code"

// WriteSuccess 写一条成功响应（HTTP 200 + Code=0），handler 用它收尾。
func WriteSuccess(c *gin.Context, data any) {
	c.Set(MetricsCodeKey, 0)
	c.JSON(http.StatusOK, SuccessResponse(data))
}

// WriteError 把 service 返的 error 转成响应信封。如果是 errcode.Error
// 直接用对应 Code / Reason + HTTPStatus；其他类型一律压成 INTERNAL_ERROR
// (HTTP 500)，避免泄漏底层错误细节给客户端。
func WriteError(c *gin.Context, err error) {
	var ec errcode.Error
	if errors.As(err, &ec) {
		c.Set(MetricsCodeKey, ec.Code())
		c.JSON(ec.HTTPStatus(), ErrorResponse(c, ec))
		return
	}
	c.Set(MetricsCodeKey, errcode.InternalError.Code())
	c.JSON(errcode.InternalError.HTTPStatus(), ErrorResponse(c, errcode.InternalError))
}

// AbortError 给 middleware 使用：写出 errcode 对应的 HTTP status + 错误信封，
// 同时阻断后续 handler，并设置 metrics 业务 code。handler 层仍优先用
// WriteError / WriteValidationError，保持调用语义清晰。
func AbortError(c *gin.Context, errorCode errcode.Error) {
	c.Set(MetricsCodeKey, errorCode.Code())
	c.AbortWithStatusJSON(errorCode.HTTPStatus(), ErrorResponse(c, errorCode))
}

// WriteValidationError 写参数校验错误响应。handler 里 ShouldBind 失败统一
// 走它，与 WriteSuccess / WriteError 三个 helper 调用形态对齐。HTTP 状态
// 锁死成 errcode.InvalidParams.HTTPStatus()（400）—— validation 失败始终
// 是客户端错误。
func WriteValidationError(c *gin.Context, err error) {
	c.Set(MetricsCodeKey, errcode.InvalidParams.Code())
	c.JSON(errcode.InvalidParams.HTTPStatus(), BuildValidationErrorResponse(c, err))
}

// MessageFor 按 errcode reason 返默认英文人读文案。
// 导出供 scripts/gen-errcodes.go 复用同一份文案表，避免重复维护。
func MessageFor(reason string) string {
	switch reason {
	case "INVALID_PARAMS":
		return "invalid request parameters"
	case "UNAUTHORIZED":
		return "unauthorized"
	case "PERMISSION_DENIED":
		return "permission denied"
	case "TOO_MANY_REQUESTS":
		return "too many requests"
	case "REQUEST_TIMEOUT":
		return "request timeout"
	case "SERVICE_DISABLED":
		return "endpoint is disabled by configuration"
	case "INTERNAL_ERROR":
		return "internal server error"
	case "DATABASE_ERROR":
		return "database error"
	case "QUEUE_UNAVAILABLE":
		return "queue unavailable"
	case "QUEUE_ERROR":
		return "queue error"
	case "NOT_IMPLEMENTED_YET":
		return "not implemented yet"
	default:
		return "operation failed"
	}
}

// buildMetadata 把 trace_id 从 gin.Context 取出来塞到响应 metadata 里，
// 让前端拿到错误响应时能直接报 trace_id 找日志。没有 trace_id 时返 nil
// 让 omitempty 把字段省掉，保持响应干净。
func buildMetadata(c *gin.Context) map[string]any {
	if traceID := c.GetString("trace_id"); traceID != "" {
		return map[string]any{"trace_id": traceID}
	}
	return nil
}
