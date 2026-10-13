package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/middleware"
)

// RouterDeps lists everything the router needs to build its handlers.
type RouterDeps struct {
	Log         *slog.Logger
	CORSOrigins []string
	Health      *HealthHandler
	Dashboard   *DashboardHandler
}

// NewRouter builds the Gin engine with shared middleware and all routes.
func NewRouter(d RouterDeps) *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(
		middleware.RequestID(),
		middleware.Logger(d.Log),
		middleware.Recovery(d.Log),
		middleware.CORS(d.CORSOrigins),
	)

	r.NoRoute(jsonError(http.StatusNotFound, "not found"))
	r.NoMethod(jsonError(http.StatusMethodNotAllowed, "method not allowed"))

	r.GET("/healthz", d.Health.Check)

	dashboard := r.Group("/api/v1/dashboard")
	dashboard.GET("/stats", d.Dashboard.Stats)
	dashboard.GET("/jobs", d.Dashboard.Jobs)
	dashboard.GET("/invoices", d.Dashboard.Invoices)
	dashboard.GET("/invoices/aging", d.Dashboard.InvoiceAging)
	dashboard.GET("/estimates", d.Dashboard.Estimates)
	dashboard.GET("/revenue-per-month", d.Dashboard.RevenuePerMonth)
	dashboard.GET("/sales-forecast", d.Dashboard.SalesForecast)
	dashboard.GET("/payments", d.Dashboard.Payments)
	dashboard.GET("/revenue-by-staff", d.Dashboard.RevenueByStaff)
	dashboard.GET("/active-services", d.Dashboard.ActiveServices)
	dashboard.GET("/tags", d.Dashboard.Tags)
	dashboard.GET("/customers-by-source", d.Dashboard.CustomersBySource)

	return r
}

func jsonError(status int, msg string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(status, dto.ErrorResponse{Error: msg, RequestID: middleware.GetRequestID(c)})
	}
}
