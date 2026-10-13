package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger writes one structured log line per request.
// 5xx responses log at Error, 4xx at Warn, everything else at Info.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
			slog.String("request_id", GetRequestID(c)),
		}
		if errs := c.Errors.String(); errs != "" {
			attrs = append(attrs, slog.String("errors", errs))
		}
		log.LogAttrs(c.Request.Context(), level, "http request", attrs...)
	}
}
