package handler

import (
	"fmt"
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
	// TrustedProxies are the IPs/CIDRs whose X-Forwarded-For is believed for the client IP. Nil trusts none.
	TrustedProxies []string
	Health         *HealthHandler
	Dashboard      *DashboardHandler
	// Recurring is optional; its routes are registered only when set.
	Recurring *RecurringHandler
}

// NewRouter builds the Gin engine with shared middleware and all routes.
func NewRouter(d RouterDeps) (*gin.Engine, error) {
	r := gin.New()
	// Gin trusts every proxy by default, which lets any client forge its logged IP with X-Forwarded-For.
	if err := r.SetTrustedProxies(d.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
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
	r.HEAD("/healthz", d.Health.Check)

	// Each route lists the query parameters it reads; any other parameter is a 400.
	rangeOnly, yearOnly := allowQuery("range"), allowQuery("year")
	dashboard := r.Group("/api/v1/dashboard")
	dashboard.GET("/stats", rangeOnly, d.Dashboard.Stats)
	dashboard.GET("/jobs", allowQuery("range", "staff_ids"), d.Dashboard.Jobs)
	dashboard.GET("/invoices", rangeOnly, d.Dashboard.Invoices)
	dashboard.GET("/invoices/aging", allowQuery(), d.Dashboard.InvoiceAging)
	dashboard.GET("/estimates", rangeOnly, d.Dashboard.Estimates)
	dashboard.GET("/revenue-per-month", yearOnly, d.Dashboard.RevenuePerMonth)
	dashboard.GET("/sales-forecast", yearOnly, d.Dashboard.SalesForecast)
	dashboard.GET("/payments", rangeOnly, d.Dashboard.Payments)
	dashboard.GET("/revenue-by-staff", rangeOnly, d.Dashboard.RevenueByStaff)
	dashboard.GET("/active-services", rangeOnly, d.Dashboard.ActiveServices)
	dashboard.GET("/tags", allowQuery("scope"), d.Dashboard.Tags)
	dashboard.GET("/customers-by-source", yearOnly, d.Dashboard.CustomersBySource)

	if d.Recurring != nil {
		registerRecurringRoutes(r, d.Recurring)
	}

	return r, nil
}

func jsonError(status int, msg string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(status, dto.ErrorResponse{Error: msg, RequestID: middleware.GetRequestID(c)})
	}
}

func registerRecurringRoutes(r *gin.Engine, h *RecurringHandler) {
	plans := r.Group("/api/v1/recurring-plans")
	// Every write needs a JSON Content-Type, so a cross-site simple request cannot reach it.
	jsonOnly := requireJSON()
	plans.POST("", jsonOnly, allowQuery(), h.Create)
	plans.GET("", allowQuery("customer_id", "status", "limit", "after"), h.List)
	plans.GET("/:id", allowQuery(), h.Get)
	plans.PATCH("/:id", jsonOnly, allowQuery(), h.Update)
	plans.POST("/:id/pause", jsonOnly, allowQuery(), h.Pause)
	plans.POST("/:id/resume", jsonOnly, allowQuery(), h.Resume)
	plans.POST("/:id/cancel", jsonOnly, allowQuery(), h.Cancel)
	window := allowQuery("from", "to")
	plans.GET("/:id/occurrences", window, h.Occurrences)
	plans.GET("/:id/projected-invoices", window, h.ProjectedInvoices)
	plans.GET("/:id/projected-estimate", window, h.ProjectedEstimate)
	r.GET("/api/v1/customers/:id/recurring-occurrences", window, h.CustomerOccurrences)
}
