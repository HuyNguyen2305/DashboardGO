// Package handler holds the Gin HTTP handlers and route wiring.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/middleware"
)

// Pinger reports whether a dependency is reachable. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler serves the health check.
type HealthHandler struct {
	db      Pinger
	timeout time.Duration
}

// NewHealthHandler builds a HealthHandler that pings db with the given timeout.
func NewHealthHandler(db Pinger, timeout time.Duration) *HealthHandler {
	return &HealthHandler{db: db, timeout: timeout}
}

// Check returns 200 when the database answers, 503 otherwise.
func (h *HealthHandler) Check(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusServiceUnavailable, dto.ErrorResponse{
			Error:     "database unavailable",
			RequestID: middleware.GetRequestID(c),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
