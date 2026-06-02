package middleware

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/pkg/auth"
	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

// authSubjectKey 是 BearerAuth 把 JWT subject 写进 gin.Context 用的 key。
// 在包内集中定义避免散在各处拼字符串拼错。
const authSubjectKey = "auth_subject"

// AuthSubject 返回 BearerAuth 已经写进 ctx 的鉴权 subject。未鉴权 / 没经过
// BearerAuth 的请求会拿到空字符串。
func AuthSubject(c *gin.Context) string {
	return c.GetString(authSubjectKey)
}

// BearerAuth 校验 Authorization 头里的 Bearer JWT，验过把 subject 存到 ctx。
//
// manager 为 nil 表示 JWT 未配置（JWT_SECRET 没设）——此时所有访问受保护
// 路由的请求统一返 UNAUTHORIZED，让 spec 和运行时行为一致；不要换成 404
// 之类的，否则前端联调拿不到准确的错误码。
func BearerAuth(manager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			response.AbortError(c, errcode.Unauthorized)
			return
		}

		claims, err := manager.ParseToken(c.GetHeader("Authorization"))
		if err != nil {
			response.AbortError(c, errcode.Unauthorized)
			return
		}

		c.Set(authSubjectKey, claims.Subject)
		c.Next()
	}
}
