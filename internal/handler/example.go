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

// ExampleHandler handles HTTP requests for examples.
type ExampleHandler struct {
	svc *service.ExampleService
}

// NewExampleHandler creates an ExampleHandler.
func NewExampleHandler(svc *service.ExampleService) *ExampleHandler {
	return &ExampleHandler{svc: svc}
}

// Create handles POST /api/v1/examples.
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

// List handles GET /api/v1/examples.
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

// EnqueueTask handles POST /api/v1/examples/tasks.
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
