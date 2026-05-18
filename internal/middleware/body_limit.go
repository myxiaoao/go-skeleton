package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes 给请求 body 套上 http.MaxBytesReader，超过 limit 字节时后续
// 读 body 的 handler 会拿到 "http: request body too large" 错误。
//
// 为什么不在中间件里直接返 413：handler 用 ShouldBindJSON 时错误会被映射成
// validator 失败，统一走 INVALID_PARAMS 响应信封，跟其他参数校验错误一致。
// 中间件只负责装上限制器，让 net/http 标准库处理上限触发，避免吞掉真实读错误。
//
// limit <= 0 表示不限。Header 限制由 http.Server.MaxHeaderBytes 单独控制。
func MaxBodyBytes(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limit > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
