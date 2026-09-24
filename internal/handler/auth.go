package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/internal/middleware"
	"go-skeleton/pkg/auth"
	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/response"
)

// AuthHandler 处理最小化的 JWT 示例流程：颁发 token + 鉴权后查询当前 subject。
type AuthHandler struct {
	manager *auth.JWTManager
	// DevTokenAvailable 控制 POST /auth/token 的可用性：值为 false 时端点仍
	// 留在 OpenAPI spec 和路由表，但 CreateToken 返回 SERVICE_DISABLED；这样
	// 客户端拿到的是契约一致的错误码而不是 404。导出字段方便路由层测试不走
	// 真正 JWT manager 就能切换该端点开关。
	DevTokenAvailable bool
}

// NewAuthHandler 构造 AuthHandler。manager 为 nil 也允许：handler 仍满足
// OpenAPI 契约（POST /auth/token 仍注册），CreateToken 在 manager 配齐之前
// 返回 SERVICE_DISABLED。
func NewAuthHandler(manager *auth.JWTManager, devTokenAvailable bool) *AuthHandler {
	return &AuthHandler{
		manager:           manager,
		DevTokenAvailable: devTokenAvailable,
	}
}

// CreateTokenReq 是颁发示例 JWT 的请求体。
type CreateTokenReq struct {
	Subject string `json:"subject" binding:"required"`
}

// CreateTokenRes 是示例 JWT 的响应体。
type CreateTokenRes struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// MeRes 是受保护示例端点 /auth/me 的响应体。
type MeRes struct {
	Subject string `json:"subject"`
}

// CreateToken 给指定 subject 颁发示例 JWT。端点由 AUTH_DEV_TOKEN_ENABLED
// 和 JWT manager 是否配置两个条件共同把关；任一缺失就返 SERVICE_DISABLED，
// 让 OpenAPI 契约与运行时行为对齐。
func (h *AuthHandler) CreateToken(c *gin.Context) {
	if h == nil || h.manager == nil || !h.DevTokenAvailable {
		response.WriteError(c, errcode.ServiceDisabled)
		return
	}

	var req CreateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.WriteValidationError(c, err)
		return
	}

	token, err := h.manager.GenerateToken(req.Subject)
	if errors.Is(err, auth.ErrMissingSubject) {
		response.WriteError(c, errcode.InvalidParams)
		return
	}
	if err != nil {
		// 其余错误（如 JWT_TTL 错配）是服务端问题，不能伪装成客户端参数错误。
		applog.FromContext(c.Request.Context()).Error("generate token", zap.Error(err))
		response.WriteError(c, errcode.InternalError)
		return
	}

	response.WriteSuccess(c, CreateTokenRes{
		AccessToken: token,
		TokenType:   "Bearer",
	})
}

// Me 返回当前有效 Bearer token 携带的 subject，用于前端"我是谁"探活。
// subject 由 BearerAuth 中间件预先写进 ctx，handler 这里只是取出来。
func (h *AuthHandler) Me(c *gin.Context) {
	response.WriteSuccess(c, MeRes{Subject: middleware.AuthSubject(c)})
}
