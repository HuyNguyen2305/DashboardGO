package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/dto"
)

// Recovery turns a panic into a logged 500 response instead of crashing the server.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.ErrorContext(c.Request.Context(), "panic recovered",
					slog.String("panic", fmt.Sprint(rec)),
					slog.String("request_id", GetRequestID(c)),
					slog.String("stack", string(debug.Stack())),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{
					Error:     "internal server error",
					RequestID: GetRequestID(c),
				})
			}
		}()
		c.Next()
	}
}
