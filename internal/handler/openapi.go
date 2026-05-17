package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-skeleton/internal/oapi"
)

// APIServer adapts the project's per-resource handlers to the
// oapi.ServerInterface generated from api/openapi.yaml.
//
// Each method delegates to an existing handler. Method signatures must match
// oapi.ServerInterface exactly; a compile-time assertion below guarantees
// drift between yaml and code surfaces as a build error.
type APIServer struct {
	Auth    *AuthHandler
	Health  *HealthHandler
	Example *ExampleHandler
	OpenAPI *OpenAPIHandler
}

// Compile-time guarantee that APIServer satisfies the generated contract.
// If this line stops compiling, run `make oapi` and reconcile signatures.
var _ oapi.ServerInterface = (*APIServer)(nil)

// GetHealth implements oapi.ServerInterface.
func (s *APIServer) GetHealth(c *gin.Context) {
	s.Health.Health(c)
}

// CreateAuthToken implements oapi.ServerInterface.
func (s *APIServer) CreateAuthToken(c *gin.Context) {
	s.Auth.CreateToken(c)
}

// GetAuthMe implements oapi.ServerInterface.
func (s *APIServer) GetAuthMe(c *gin.Context) {
	s.Auth.Me(c)
}

// ListExamples implements oapi.ServerInterface. Params are re-bound by the
// underlying handler via ShouldBindQuery, so we ignore the decoded struct
// and pass the raw context through.
func (s *APIServer) ListExamples(c *gin.Context, _ oapi.ListExamplesParams) {
	s.Example.List(c)
}

// CreateExample implements oapi.ServerInterface.
func (s *APIServer) CreateExample(c *gin.Context) {
	s.Example.Create(c)
}

// EnqueueExampleTask implements oapi.ServerInterface.
func (s *APIServer) EnqueueExampleTask(c *gin.Context) {
	s.Example.EnqueueTask(c)
}

// GetOpenAPISpec implements oapi.ServerInterface.
func (s *APIServer) GetOpenAPISpec(c *gin.Context) {
	s.OpenAPI.Spec(c)
}

// OpenAPIHandler serves the embedded OpenAPI 3.1 spec as JSON.
type OpenAPIHandler struct{}

// NewOpenAPIHandler creates an OpenAPIHandler.
func NewOpenAPIHandler() *OpenAPIHandler {
	return &OpenAPIHandler{}
}

// Spec returns the embedded OpenAPI 3.1 specification as JSON.
func (h *OpenAPIHandler) Spec(c *gin.Context) {
	raw, err := oapi.GetSpecJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load openapi spec"})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}
