package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

// Timeout 给 request ctx 挂 deadline。deadline 在 handler 写响应之前触发时，
// 中间件把失败统一映射成 REQUEST_TIMEOUT 信封，让客户端拿到稳定的错误码，
// 而不是各种下游冒上来的 raw 错误消息。
//
// **不适用于 streaming / SSE handler。** Gin 的 c.Next() 是同步阻塞直到 handler
// 返回，本中间件依赖 handler 返回后再检查 c.Writer.Written()。streaming handler
// 在写出首字节后会让 Written() 提前为 true，即便 ctx 已超时也不会触发本中间件
// 的错误信封，客户端可能收到被截断的响应而没有任何错误码。streaming endpoint
// 应该自己处理 ctx.Done() 并发出业务定义的 SSE 错误事件，**不要**叠加本中间件。
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeout <= 0 {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		if c.Writer.Written() {
			return
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.AbortWithStatusJSON(200, response.ErrorResponse(c, errcode.RequestTimeout))
		}
	}
}
