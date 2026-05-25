package router

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/internal/handler"
)

// Dependencies 收拢路由注册阶段需要的 handler 实例和中间件。
//
// 新增模块时不要手改这里，跑 scripts/new-endpoint.sh <Name>——脚本按文
// 件里以 NEH 前缀打头的锚点行（如 "NEH deps-fields"）注入字段和注册调用。
// 锚点行的格式与位置都不要乱动，否则下次再跑脚本注入会失败。
type Dependencies struct {
	Auth         *handler.AuthHandler
	AuthRequired gin.HandlerFunc
	Example      *handler.ExampleHandler
	// NEH deps-fields
}

// RegisterRoutes 把所有 API 路由注册到 r 这个 RouterGroup 下。返回 error
// 是预留口子（未来某条路由注册可能失败），目前总是返 nil。
func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	registerAuthRoutes(r, deps)
	registerExampleRoutes(r, deps)
	// NEH routes-register
	return nil
}

// registerAuthRoutes 挂 /auth/* 路由。/auth/token 故意不在 BearerAuth 后面
// 注册——它本身就是颁发 token 的入口；/auth/me 才走 AuthRequired。
func registerAuthRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.Auth == nil {
		return
	}

	authRoutes := r.Group("/auth")
	// POST /auth/token 始终注册，保持 OpenAPI spec 与运行时路由一致。当 JWT
	// manager 缺失或 dev-token 端点被关时，handler 返 SERVICE_DISABLED 而
	// 不是 404（见 AuthHandler.CreateToken）。
	authRoutes.POST("/token", deps.Auth.CreateToken)
	if deps.AuthRequired != nil {
		authRoutes.GET("/me", deps.AuthRequired, deps.Auth.Me)
	}
}

// registerExampleRoutes 挂 /examples/* 路由：列表 / 创建 / 入队任务。
// 这里没绑 AuthRequired——是骨架示例，业务接 endpoint 时按需加。
func registerExampleRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.Example == nil {
		return
	}

	examples := r.Group("/examples")
	examples.GET("", deps.Example.List)
	examples.POST("", deps.Example.Create)
	examples.POST("/tasks", deps.Example.EnqueueTask)
}
