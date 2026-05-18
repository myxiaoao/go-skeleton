package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

// TraceLogger 给每个请求绑定 trace_id（取自 X-Request-ID 头，没有就生成
// UUID），并按 auditEnabled / auditExcludes 决定要不要打审计日志。
//
// trace_id 同时写进 gin.Context（"trace_id" 键）、响应头（X-Request-ID）
// 和 request context（applog.WithTraceID）—— 让下游 service / repository
// 通过 applog.FromContext(ctx) 拿到的 logger 自带 trace_id 字段。
//
// auditExcludes 用于跳过 /health、/livez 这类高频探活路径，避免日志被刷屏。
func TraceLogger(auditEnabled bool, auditExcludes []string) gin.HandlerFunc {
	// 启动时把 excludes 切片转成 map，命中判断 O(1)；每个请求都做一遍切片
	// 线性扫描在 QPS 高时会成为可观察的开销。
	excludes := make(map[string]struct{}, len(auditExcludes))
	for _, path := range auditExcludes {
		if path != "" {
			excludes[path] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		start := time.Now()
		// 复用上游传入的 X-Request-ID 头（比如网关已经分配过 trace_id），让全链
		// 路 trace 拼得起来；上游没传才自己生成。
		traceID := c.GetHeader("X-Request-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		c.Set("trace_id", traceID)
		c.Header("X-Request-ID", traceID)
		c.Request = c.Request.WithContext(applog.WithTraceID(c.Request.Context(), traceID))

		c.Next()

		if !auditEnabled {
			return
		}
		if _, skip := excludes[c.Request.URL.Path]; skip {
			return
		}
		applog.FromContext(c.Request.Context()).Info("http request completed",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}
