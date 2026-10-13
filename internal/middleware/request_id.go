// Package middleware holds Gin middleware shared by all routes.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-ID"

const requestIDKey = "request_id"

// Incoming IDs are echoed into logs and responses, so only accept safe characters.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID reuses a valid incoming X-Request-ID or generates a new one,
// stores it on the context and echoes it in the response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		c.Set(requestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// GetRequestID returns the ID set by RequestID, or "" if the middleware did not run.
func GetRequestID(c *gin.Context) string {
	return c.GetString(requestIDKey)
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24.
	return hex.EncodeToString(b)
}
