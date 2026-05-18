package handler

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/internal/middleware"
	"go-skeleton/pkg/auth"
	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

// AuthHandler handles the minimal JWT example flow.
type AuthHandler struct {
	manager *auth.JWTManager
	// DevTokenAvailable governs POST /auth/token: when false, the endpoint
	// stays in the OpenAPI spec and the routing table, but CreateToken
	// returns SERVICE_DISABLED so clients see a spec-aligned error instead
	// of 404. Exposed so router-level tests can flip it without going through
	// the JWT manager.
	DevTokenAvailable bool
}

// NewAuthHandler creates an AuthHandler. A nil manager is allowed: the
// resulting handler still satisfies the OpenAPI contract (POST /auth/token
// stays routed) but CreateToken will return SERVICE_DISABLED until a JWT
// manager is configured.
func NewAuthHandler(manager *auth.JWTManager, devTokenAvailable bool) *AuthHandler {
	return &AuthHandler{
		manager:           manager,
		DevTokenAvailable: devTokenAvailable,
	}
}

// CreateTokenReq is the request body for issuing a sample JWT.
type CreateTokenReq struct {
	Subject string `json:"subject" binding:"required"`
}

// CreateTokenRes is the response body for a sample JWT.
type CreateTokenRes struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// MeRes is the response body for the protected auth example.
type MeRes struct {
	Subject string `json:"subject"`
}

// CreateToken issues a sample JWT for the given subject. The endpoint is
// gated by AUTH_DEV_TOKEN_ENABLED and by the presence of a JWT manager;
// when either is missing, it returns SERVICE_DISABLED so the OpenAPI contract
// and runtime behavior stay aligned.
func (h *AuthHandler) CreateToken(c *gin.Context) {
	if h == nil || h.manager == nil || !h.DevTokenAvailable {
		response.WriteError(c, errcode.ServiceDisabled)
		return
	}

	var req CreateTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, response.BuildValidationErrorResponse(c, err))
		return
	}

	token, err := h.manager.GenerateToken(req.Subject)
	if err != nil {
		response.WriteError(c, errcode.InvalidParams)
		return
	}

	response.WriteSuccess(c, CreateTokenRes{
		AccessToken: token,
		TokenType:   "Bearer",
	})
}

// Me returns the subject from a valid Bearer token.
func (h *AuthHandler) Me(c *gin.Context) {
	response.WriteSuccess(c, MeRes{Subject: middleware.AuthSubject(c)})
}
