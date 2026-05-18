package handler

// Example handler 教学模板：handler 层只做三件事
//
//  1. 参数绑定 / 校验（c.ShouldBind...，失败走 response.BuildValidationErrorResponse）。
//  2. 调 service（传 c.Request.Context()，不要传 *gin.Context）。
//  3. 用 response.WriteSuccess / response.WriteError 转协议。
//
// **禁止**在 handler 里写业务规则、拼接错误字符串、直接连数据库。
// 复制这个文件做新 endpoint 时，先在 api/openapi.yaml 加路径 + 跑 make oapi，
// 再补 handler → service → repository → model。

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/internal/service"
	"go-skeleton/pkg/response"
)

// ExampleHandler 处理 example 资源的 HTTP 请求。所有业务规则委托给 svc，
// 自己只做参数绑定 + 响应包装。
type ExampleHandler struct {
	svc *service.ExampleService
}

// NewExampleHandler 构造 ExampleHandler。svc 由 internal/server.go 装配。
func NewExampleHandler(svc *service.ExampleService) *ExampleHandler {
	return &ExampleHandler{svc: svc}
}

// Create 处理 POST /api/v1/examples：创建一条 example 记录。
func (h *ExampleHandler) Create(c *gin.Context) {
	var req service.CreateExampleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, response.BuildValidationErrorResponse(c, err))
		return
	}

	example, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		response.WriteError(c, err)
		return
	}

	response.WriteSuccess(c, example)
}

// List 处理 GET /api/v1/examples：返回分页列表 + total（近似值，
// 见 repository.List 的快照一致性说明）。
func (h *ExampleHandler) List(c *gin.Context) {
	var req service.ListExamplesReq
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, response.BuildValidationErrorResponse(c, err))
		return
	}

	res, err := h.svc.List(c.Request.Context(), &req)
	if err != nil {
		response.WriteError(c, err)
		return
	}

	response.WriteSuccess(c, res)
}

// EnqueueTask 处理 POST /api/v1/examples/tasks：把示例任务推到 Asynq 队列，
// 真正的执行逻辑在 internal/worker 包里消费。队列没配置时返 QUEUE_UNAVAILABLE。
func (h *ExampleHandler) EnqueueTask(c *gin.Context) {
	var req service.EnqueueExampleTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, response.BuildValidationErrorResponse(c, err))
		return
	}

	res, err := h.svc.EnqueueTask(c.Request.Context(), &req)
	if err != nil {
		response.WriteError(c, err)
		return
	}

	response.WriteSuccess(c, res)
}
