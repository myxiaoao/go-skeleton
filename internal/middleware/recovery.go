package middleware

import (
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/response"
)

// Recovery catches panics and returns the standard API error envelope.
//
// applog.FromContext 已经会把 ctx 里的 trace_id 注入 logger，这里再显式
// zap.String 一次是冗余兜底——如果有人调换中间件顺序把 Recovery 排到
// TraceLogger 之前，FromContext 会拿不到 ctx 里的 trace_id，但 c.GetString
// 也是空，至少日志字段始终存在（值为空），便于采集端用同一 query 抓 panic。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				applog.FromContext(c.Request.Context()).Error("panic recovered",
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
					zap.String("trace_id", c.GetString("trace_id")),
					zap.Any("error", err),
					zap.ByteString("stacktrace", debug.Stack()),
				)
				c.AbortWithStatusJSON(200, response.ErrorResponse(c, errcode.InternalError))
			}
		}()
		c.Next()
	}
}
