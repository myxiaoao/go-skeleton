package middleware

import (
	"time"
	"uuid"

	"github.com/gin-gonic/gin"
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
		if !validRequestID(traceID) {
			traceID = uuid.New().String()
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

// maxRequestIDLen 限制客户端传入 X-Request-ID 的长度：它会原样写进响应头和
// 每条日志，不设上限等于让客户端控制日志体积。
const maxRequestIDLen = 128

// validRequestID 只接受非空、不超长、由字母数字和 -_.: 组成的 ID（覆盖 UUID、
// 常见网关 trace 格式），其余一律丢弃重新生成，避免日志字段被注入污染。
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := range len(id) {
		ch := id[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-', ch == '_', ch == '.', ch == ':':
		default:
			return false
		}
	}
	return true
}
