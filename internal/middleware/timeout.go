package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

// Timeout attaches a deadline to request contexts. When the deadline trips
// before a handler writes a response, it maps the failure to the standard
// REQUEST_TIMEOUT envelope so clients see a consistent error code instead of
// whichever downstream message bubbled up.
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeout <= 0 {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		if c.Writer.Written() {
			return
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.AbortWithStatusJSON(200, response.ErrorResponse(c, errcode.RequestTimeout))
		}
	}
}
