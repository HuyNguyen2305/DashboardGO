package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/model"
)

// maxStaffIDs caps the Schedules filter so a request can't build an unbounded ANY() list.
const maxStaffIDs = 50

// DashboardService is what the dashboard handlers need from the service layer.
type DashboardService interface {
	Stats(ctx context.Context, r daterange.Range) (model.DashboardStats, error)
	Jobs(ctx context.Context, r daterange.Range, staff model.StaffFilter) (model.JobsWidget, error)
	Invoices(ctx context.Context, r daterange.Range) (model.Breakdown, error)
	Estimates(ctx context.Context, r daterange.Range) (model.Breakdown, error)
	InvoiceAging(ctx context.Context, asOf time.Time) (model.AgingWidget, error)
	RevenuePerMonth(ctx context.Context, r daterange.Range) (model.YearSeries, error)
	SalesForecast(ctx context.Context, r daterange.Range) (model.ForecastSeries, error)
	Payments(ctx context.Context, r daterange.Range) (model.Breakdown, error)
	RevenueByStaff(ctx context.Context, r daterange.Range) (model.StaffRevenue, error)
	ActiveServices(ctx context.Context, r daterange.Range) (model.ActiveServices, error)
	Tags(ctx context.Context, scope string) (model.TagUsage, error)
	CustomersBySource(ctx context.Context, r daterange.Range) (model.SourceSeries, error)
}

// DashboardHandler serves the dashboard widgets.
type DashboardHandler struct {
	svc   DashboardService
	dates *daterange.Resolver
}

// NewDashboardHandler builds a DashboardHandler.
func NewDashboardHandler(svc DashboardService, dates *daterange.Resolver) *DashboardHandler {
	return &DashboardHandler{svc: svc, dates: dates}
}

// Stats serves GET /api/v1/dashboard/stats?range=<preset>.
func (h *DashboardHandler) Stats(c *gin.Context) {
	r, err := ParseRange(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}

	// Stats count what has happened, so rows dated later in the period are left out.
	s, err := h.svc.Stats(c.Request.Context(), r.CapAt(h.dates.Now()))
	if err != nil {
		WriteError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.StatsResponse{
		Range:               NewDateRange(r),
		Revenue:             dto.Money(s.Revenue),
		Collected:           dto.Money(s.Collected),
		JobsCompleted:       s.JobsCompleted,
		NewJobsBookedOnline: dto.Money(s.NewJobsBookedOnline),
		TotalNewJobs:        dto.Money(s.TotalNewJobs),
	})
}

// Jobs serves GET /api/v1/dashboard/jobs?range=...&staff_ids=1&staff_ids=2 (or staff_ids=1,2,unassigned).
func (h *DashboardHandler) Jobs(c *gin.Context) {
	r, err := ParseRange(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	staff, err := parseStaffFilter(c)
	if err != nil {
		WriteError(c, err)
		return
	}

	w, err := h.svc.Jobs(c.Request.Context(), r, staff)
	if err != nil {
		WriteError(c, err)
		return
	}

	daily := make([]dto.DailyCounts, len(w.Daily))
	for i, d := range w.Daily {
		daily[i] = dto.DailyCounts{Date: d.Date.Format("2006-01-02"), Counts: d.Counts}
	}
	c.JSON(http.StatusOK, dto.JobsResponse{BreakdownResponse: newBreakdownResponse(r, w.Breakdown), Daily: daily})
}

// Invoices serves GET /api/v1/dashboard/invoices?range=...
func (h *DashboardHandler) Invoices(c *gin.Context) {
	h.breakdown(c, h.svc.Invoices)
}

// Estimates serves GET /api/v1/dashboard/estimates?range=...
func (h *DashboardHandler) Estimates(c *gin.Context) {
	h.breakdown(c, h.svc.Estimates)
}

// Payments serves GET /api/v1/dashboard/payments?range=...
func (h *DashboardHandler) Payments(c *gin.Context) {
	h.breakdown(c, h.svc.Payments)
}

func (h *DashboardHandler) breakdown(c *gin.Context, load func(context.Context, daterange.Range) (model.Breakdown, error)) {
	r, err := ParseRange(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	// Invoices, estimates and payments dated later in the period haven't happened yet.
	b, err := load(c.Request.Context(), r.CapAt(h.dates.Now()))
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, newBreakdownResponse(r, b))
}

// InvoiceAging serves GET /api/v1/dashboard/invoices/aging. It always uses today in the business timezone.
func (h *DashboardHandler) InvoiceAging(c *gin.Context) {
	w, err := h.svc.InvoiceAging(c.Request.Context(), h.dates.Today())
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.AgingResponse{
		AsOf:        w.AsOf.Format("2006-01-02"),
		TotalAmount: dto.Money(w.TotalAmount),
		TotalCount:  w.TotalCount,
		Rows:        newBreakdownRows(w.Rows),
	})
}

// RevenuePerMonth serves GET /api/v1/dashboard/revenue-per-month?year=2026.
func (h *DashboardHandler) RevenuePerMonth(c *gin.Context) {
	h.yearSeries(c, h.svc.RevenuePerMonth)
}

// SalesForecast serves GET /api/v1/dashboard/sales-forecast?year=2026.
func (h *DashboardHandler) SalesForecast(c *gin.Context) {
	r, err := ParseYear(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	s, err := h.svc.SalesForecast(c.Request.Context(), r)
	if err != nil {
		WriteError(c, err)
		return
	}

	months := make([]dto.ForecastMonth, len(s.Months))
	for i, m := range s.Months {
		months[i] = dto.ForecastMonth{
			Month:     m.Month.Format("2006-01"),
			Amount:    dto.Money(m.Booked),
			Recurring: dto.Money(m.Recurring),
		}
	}
	c.JSON(http.StatusOK, dto.SalesForecastResponse{
		Range:          NewDateRange(r),
		Total:          dto.Money(s.BookedTotal),
		RecurringTotal: dto.Money(s.RecurringTotal),
		Months:         months,
	})
}

func (h *DashboardHandler) yearSeries(c *gin.Context, load func(context.Context, daterange.Range) (model.YearSeries, error)) {
	r, err := ParseYear(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	s, err := load(c.Request.Context(), r)
	if err != nil {
		WriteError(c, err)
		return
	}

	months := make([]dto.MonthlyAmount, len(s.Months))
	for i, m := range s.Months {
		months[i] = dto.MonthlyAmount{Month: m.Month.Format("2006-01"), Amount: dto.Money(m.Amount)}
	}
	c.JSON(http.StatusOK, dto.YearSeriesResponse{Range: NewDateRange(r), Total: dto.Money(s.Total), Months: months})
}

// RevenueByStaff serves GET /api/v1/dashboard/revenue-by-staff?range=...
func (h *DashboardHandler) RevenueByStaff(c *gin.Context) {
	r, err := ParseRange(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	w, err := h.svc.RevenueByStaff(c.Request.Context(), r.CapAt(h.dates.Now()))
	if err != nil {
		WriteError(c, err)
		return
	}

	rows := make([]dto.StaffRevenueRow, len(w.Rows))
	for i, row := range w.Rows {
		rows[i] = dto.StaffRevenueRow{
			StaffID: row.StaffID, Name: row.Name, AvatarURL: row.AvatarURL,
			Count: row.Count, Amount: dto.Money(row.Amount), Percent: row.Percent,
		}
	}
	c.JSON(http.StatusOK, dto.StaffRevenueResponse{Range: NewDateRange(r), TotalAmount: dto.Money(w.TotalAmount), Rows: rows})
}

// ActiveServices serves GET /api/v1/dashboard/active-services?range=...
func (h *DashboardHandler) ActiveServices(c *gin.Context) {
	r, err := ParseRange(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	w, err := h.svc.ActiveServices(c.Request.Context(), r)
	if err != nil {
		WriteError(c, err)
		return
	}

	rows := make([]dto.ServiceRow, len(w.Rows))
	for i, row := range w.Rows {
		rows[i] = dto.ServiceRow{
			ServiceID: row.ServiceID, Name: row.Name, IsRecurring: row.IsRecurring,
			Count: row.Count, CustomerCount: row.CustomerCount, Amount: dto.Money(row.Amount), Percent: row.Percent,
		}
	}
	c.JSON(http.StatusOK, dto.ActiveServicesResponse{
		Range:                  NewDateRange(r),
		TotalCount:             w.TotalCount,
		RecurringCount:         w.RecurringCount,
		CustomerCount:          w.CustomerCount,
		RecurringCustomerCount: w.RecurringCustomerCount,
		TotalAmount:            dto.Money(w.TotalAmount),
		Rows:                   rows,
	})
}

// Tags serves GET /api/v1/dashboard/tags?scope=location|customer (default location). It has no date filter.
func (h *DashboardHandler) Tags(c *gin.Context) {
	// An empty ?scope= means the default too; DefaultQuery only covers a missing one.
	scope := c.Query("scope")
	if scope == "" {
		scope = model.TagScopeLocation
	}
	w, err := h.svc.Tags(c.Request.Context(), scope)
	if err != nil {
		WriteError(c, err)
		return
	}

	rows := make([]dto.TagRow, len(w.Rows))
	for i, row := range w.Rows {
		rows[i] = dto.TagRow{TagID: row.TagID, Name: row.Name, Count: row.Count, CustomerCount: row.CustomerCount, Percent: row.Percent}
	}
	c.JSON(http.StatusOK, dto.TagsResponse{Scope: w.Scope, TotalCount: w.TotalCount, Rows: rows})
}

// CustomersBySource serves GET /api/v1/dashboard/customers-by-source?year=2026.
func (h *DashboardHandler) CustomersBySource(c *gin.Context) {
	r, err := ParseYear(c, h.dates)
	if err != nil {
		WriteError(c, err)
		return
	}
	s, err := h.svc.CustomersBySource(c.Request.Context(), r)
	if err != nil {
		WriteError(c, err)
		return
	}

	months := make([]dto.MonthlySourceCounts, len(s.Months))
	for i, m := range s.Months {
		months[i] = dto.MonthlySourceCounts{Month: m.Month.Format("2006-01"), Counts: m.Counts}
	}
	sources := s.Sources
	if sources == nil {
		sources = []string{}
	}
	c.JSON(http.StatusOK, dto.CustomersBySourceResponse{
		Range: NewDateRange(r), TotalCount: s.TotalCount, Sources: sources, Months: months,
	})
}

func newBreakdownResponse(r daterange.Range, b model.Breakdown) dto.BreakdownResponse {
	return dto.BreakdownResponse{
		Range:       NewDateRange(r),
		TotalAmount: dto.Money(b.TotalAmount),
		TotalCount:  b.TotalCount,
		Rows:        newBreakdownRows(b.Rows),
	}
}

// newBreakdownRows always returns a non-nil slice so empty widgets encode as [] rather than null.
func newBreakdownRows(rows []model.BreakdownRow) []dto.BreakdownRow {
	out := make([]dto.BreakdownRow, len(rows))
	for i, row := range rows {
		out[i] = dto.BreakdownRow{Key: row.Key, Count: row.Count, Amount: dto.Money(row.Amount), Percent: row.Percent}
	}
	return out
}

// unassignedStaff is the staff_ids value that selects jobs with no staff.
const unassignedStaff = "unassigned"

// parseStaffFilter reads staff_ids as repeated params and/or comma-separated lists of staff IDs
// and the word "unassigned". No staff_ids means all schedules. Duplicates are dropped.
func parseStaffFilter(c *gin.Context) (model.StaffFilter, error) {
	var f model.StaffFilter
	seen := make(map[int64]bool)
	for _, raw := range c.QueryArray("staff_ids") {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part == unassignedStaff {
				f.Unassigned = true
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 {
				return model.StaffFilter{}, fmt.Errorf("%w: staff_ids must be positive whole numbers or %q, got %q",
					ErrInvalidQuery, unassignedStaff, part)
			}
			if !seen[id] {
				seen[id] = true
				f.IDs = append(f.IDs, id)
			}
		}
	}
	if len(f.IDs) > maxStaffIDs {
		return model.StaffFilter{}, fmt.Errorf("%w: at most %d staff_ids allowed, got %d", ErrInvalidQuery, maxStaffIDs, len(f.IDs))
	}
	return f, nil
}
