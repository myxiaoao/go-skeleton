package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 返回一个基于白名单的简版 CORS 中间件，只允许 allowOrigins 列出的
// Origin。Origin 头匹配时写 Allow-Origin / Allow-Methods / Allow-Headers；
// 不匹配时不写头，让浏览器原生拦下。OPTIONS 预检请求直接返 204。
//
// allowCredentials 控制是否带 Access-Control-Allow-Credentials: true。
// 本骨架默认是无状态 JWT API（Authorization 头），不需要 cookie，所以默认
// 关闭——避免与跨 origin 服务共用 Redis / cookie 时被意外携带凭证。前端
// 真要从浏览器带 cookie 才打开。
func CORS(allowOrigins []string, allowCredentials bool) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowOrigins))
	for _, origin := range allowOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && containsOrigin(allowed, origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin,Content-Type,Accept,Authorization,X-Request-ID")
			if allowCredentials {
				c.Header("Access-Control-Allow-Credentials", "true")
			}
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// containsOrigin 判断 origin 是否在白名单内。抽成独立函数是为了将来要换
// 通配符 / 正则匹配时单点改这里。
func containsOrigin(allowed map[string]struct{}, origin string) bool {
	_, ok := allowed[origin]
	return ok
}
