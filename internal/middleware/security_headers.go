package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 写一组对 JSON API 安全的标配响应头，覆盖所有响应。
//
// 选项很克制：
//   - X-Content-Type-Options: nosniff —— 阻止浏览器把 application/json 当
//     text/html 嗅探解释（XSS 风险）。
//   - X-Frame-Options: DENY —— API 不应被嵌 iframe；clickjacking 兜底。
//   - Referrer-Policy: no-referrer —— API 响应不带 Referer，避免泄漏 trace_id
//     等给跨站请求。
//
// **不**加 Strict-Transport-Security：HSTS 要由 LB / 反代根据 TLS 终止位置写，
// 业务代码上没有可靠的 https 信号（X-Forwarded-Proto 可被伪造）。
// **不**加 Content-Security-Policy：纯 JSON API 不渲染 HTML，CSP 无意义；
// 强加上反而会让前端联调 /openapi.json 时被浏览器拦。
func SecurityHeaders(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enabled {
			c.Header("X-Content-Type-Options", "nosniff")
			c.Header("X-Frame-Options", "DENY")
			c.Header("Referrer-Policy", "no-referrer")
		}
		c.Next()
	}
}
