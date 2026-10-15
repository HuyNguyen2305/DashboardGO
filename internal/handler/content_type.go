package handler

import (
	"fmt"
	"mime"

	"github.com/gin-gonic/gin"
)

// requireJSON rejects a request whose Content-Type media type is not application/json with 415,
// parameters such as charset allowed. Applied to every write route, even body-less ones: a
// browser sends a cross-site form or text/plain POST without a CORS preflight, but never a
// cross-site application/json one, so this keeps other sites from triggering writes.
func requireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			WriteError(c, fmt.Errorf("content type %q: %w", c.GetHeader("Content-Type"), ErrUnsupportedMediaType))
			return
		}
		c.Next()
	}
}
