package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
)

// CORS allows browser requests from the listed origins only.
// Preflight requests from other origins get 403.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := origin != "" && slices.Contains(allowedOrigins, origin)
		preflight := c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != ""

		// Every response depends on Origin, allowed or not. Without Vary, a shared cache could serve
		// a copy without CORS headers to the allowed frontend.
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		if preflight {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
		}

		if allowed {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", RequestIDHeader)
		}

		if preflight {
			if !allowed {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			// The API is read-only.
			h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+RequestIDHeader)
			h.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
