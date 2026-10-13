// Package repository implements data access on top of the sqlc-generated queries.
package repository

import (
	"context"
	"fmt"
	"time"

	"dashboardgo/internal/model"
	"dashboardgo/internal/repository/db"
)

// DashboardRepository reads dashboard aggregates.
type DashboardRepository struct {
	q       *db.Queries
	timeout time.Duration
}

// NewDashboardRepository uses conn (usually the shared pool) and caps every query at timeout.
func NewDashboardRepository(conn db.DBTX, timeout time.Duration) *DashboardRepository {
	return &DashboardRepository{q: db.New(conn), timeout: timeout}
}

// Stats returns the top Stats row in a single query.
func (r *DashboardRepository) Stats(ctx context.Context, sq model.StatsQuery) (model.DashboardStats, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	row, err := r.q.GetDashboardStats(ctx, db.GetDashboardStatsParams{
		StartAt:                  sq.Start,
		EndAt:                    sq.End,
		RevenueStatuses:          sq.RevenueStatuses,
		ForecastEstimateStatuses: sq.ForecastEstimateStatuses,
		ForecastInvoiceStatuses:  sq.ForecastInvoiceStatuses,
	})
	if err != nil {
		return model.DashboardStats{}, fmt.Errorf("get dashboard stats: %w", err)
	}
	return model.DashboardStats{
		Revenue:             row.Revenue,
		Collected:           row.Collected,
		JobsCompleted:       row.JobsCompleted,
		NewJobsBookedOnline: row.NewJobsBookedOnline,
		TotalNewJobs:        row.TotalNewJobs,
	}, nil
}

// JobStatusBreakdown returns job count and amount per status.
func (r *DashboardRepository) JobStatusBreakdown(ctx context.Context, q model.JobsQuery) ([]model.BreakdownRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.JobStatusBreakdown(ctx, db.JobStatusBreakdownParams{
		StartAt: q.Start, EndAt: q.End, StaffIds: nonNil(q.StaffIDs),
	})
	if err != nil {
		return nil, fmt.Errorf("job status breakdown: %w", err)
	}
	out := make([]model.BreakdownRow, len(rows))
	for i, row := range rows {
		out[i] = model.BreakdownRow{Key: row.Status, Count: row.Count, Amount: row.Amount}
	}
	return out, nil
}

// JobDailyCounts returns job counts per local day and status, only for days that have jobs.
func (r *DashboardRepository) JobDailyCounts(ctx context.Context, q model.JobsQuery) ([]model.DayStatusCount, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.JobDailyCounts(ctx, db.JobDailyCountsParams{
		StartAt: q.Start, EndAt: q.End, Tz: q.TZ, StaffIds: nonNil(q.StaffIDs),
	})
	if err != nil {
		return nil, fmt.Errorf("job daily counts: %w", err)
	}
	out := make([]model.DayStatusCount, len(rows))
	for i, row := range rows {
		out[i] = model.DayStatusCount{Day: row.Day, Status: row.Status, Count: row.Count}
	}
	return out, nil
}

// DocumentStatusBreakdown returns count and amount per status for invoices or estimates.
func (r *DashboardRepository) DocumentStatusBreakdown(ctx context.Context, q model.DocumentsQuery) ([]model.BreakdownRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.DocumentStatusBreakdown(ctx, db.DocumentStatusBreakdownParams{
		DocType: q.DocType, StartAt: q.Start, EndAt: q.End,
	})
	if err != nil {
		return nil, fmt.Errorf("%s status breakdown: %w", q.DocType, err)
	}
	out := make([]model.BreakdownRow, len(rows))
	for i, row := range rows {
		out[i] = model.BreakdownRow{Key: row.Status, Count: row.Count, Amount: row.Amount}
	}
	return out, nil
}

// InvoiceAging returns outstanding invoice count and balance per aging bucket.
func (r *DashboardRepository) InvoiceAging(ctx context.Context, q model.AgingQuery) ([]model.BreakdownRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.InvoiceAging(ctx, db.InvoiceAgingParams{AsOf: q.AsOf, Tz: q.TZ, Statuses: q.Statuses})
	if err != nil {
		return nil, fmt.Errorf("invoice aging: %w", err)
	}
	out := make([]model.BreakdownRow, len(rows))
	for i, row := range rows {
		out[i] = model.BreakdownRow{Key: row.Bucket, Count: row.Count, Amount: row.Amount}
	}
	return out, nil
}

// MonthlyRevenue returns revenue per local month, only for months that have revenue.
func (r *DashboardRepository) MonthlyRevenue(ctx context.Context, q model.MonthlyQuery) ([]model.MonthlyAmount, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.MonthlyRevenue(ctx, db.MonthlyRevenueParams{
		StartAt: q.Start, EndAt: q.End, Tz: q.TZ, RevenueStatuses: q.RevenueStatuses,
	})
	if err != nil {
		return nil, fmt.Errorf("monthly revenue: %w", err)
	}
	out := make([]model.MonthlyAmount, len(rows))
	for i, row := range rows {
		out[i] = model.MonthlyAmount{Month: row.Month, Amount: row.Amount}
	}
	return out, nil
}

// MonthlyForecast returns the sales forecast per local month, only for months that have data.
func (r *DashboardRepository) MonthlyForecast(ctx context.Context, q model.MonthlyQuery) ([]model.MonthlyAmount, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.MonthlyForecast(ctx, db.MonthlyForecastParams{
		StartAt: q.Start, EndAt: q.End, Tz: q.TZ,
		ForecastEstimateStatuses: q.ForecastEstimateStatuses,
		ForecastInvoiceStatuses:  q.ForecastInvoiceStatuses,
	})
	if err != nil {
		return nil, fmt.Errorf("monthly forecast: %w", err)
	}
	out := make([]model.MonthlyAmount, len(rows))
	for i, row := range rows {
		out[i] = model.MonthlyAmount{Month: row.Month, Amount: row.Amount}
	}
	return out, nil
}

// PaymentsByMethod returns payment count and amount per method, biggest amount first.
func (r *DashboardRepository) PaymentsByMethod(ctx context.Context, q model.PeriodQuery) ([]model.BreakdownRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.PaymentsByMethod(ctx, db.PaymentsByMethodParams{StartAt: q.Start, EndAt: q.End})
	if err != nil {
		return nil, fmt.Errorf("payments by method: %w", err)
	}
	out := make([]model.BreakdownRow, len(rows))
	for i, row := range rows {
		out[i] = model.BreakdownRow{Key: row.Method, Count: row.Count, Amount: row.Amount}
	}
	return out, nil
}

// RevenueByStaff returns revenue per staff member, biggest first. A nil StaffID is the "Other" row.
func (r *DashboardRepository) RevenueByStaff(ctx context.Context, q model.StatsQuery) ([]model.StaffRevenueRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.RevenueByStaff(ctx, db.RevenueByStaffParams{
		StartAt: q.Start, EndAt: q.End, RevenueStatuses: q.RevenueStatuses,
	})
	if err != nil {
		return nil, fmt.Errorf("revenue by staff: %w", err)
	}
	out := make([]model.StaffRevenueRow, len(rows))
	for i, row := range rows {
		out[i] = model.StaffRevenueRow{
			StaffID: row.StaffID, Name: deref(row.StaffName), AvatarURL: row.AvatarUrl,
			Count: row.Count, Amount: row.Amount,
		}
	}
	return out, nil
}

// ActiveServices returns jobs in the given statuses per service, biggest first. A nil ServiceID is "No service".
func (r *DashboardRepository) ActiveServices(ctx context.Context, q model.ServicesQuery) ([]model.ServiceRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.ActiveServices(ctx, db.ActiveServicesParams{StartAt: q.Start, EndAt: q.End, Statuses: q.Statuses})
	if err != nil {
		return nil, fmt.Errorf("active services: %w", err)
	}
	out := make([]model.ServiceRow, len(rows))
	for i, row := range rows {
		out[i] = model.ServiceRow{
			ServiceID: row.ServiceID, Name: deref(row.ServiceName), IsRecurring: row.IsRecurring,
			Count: row.Count, Amount: row.Amount,
		}
	}
	return out, nil
}

// TagUsage returns how often each tag in use is applied within scope, most used first.
func (r *DashboardRepository) TagUsage(ctx context.Context, scope string) ([]model.TagRow, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var out []model.TagRow
	switch scope {
	case model.TagScopeCustomer:
		rows, err := r.q.CustomerTagUsage(ctx)
		if err != nil {
			return nil, fmt.Errorf("customer tag usage: %w", err)
		}
		for _, row := range rows {
			out = append(out, model.TagRow{TagID: row.ID, Name: row.Name, Count: row.Count})
		}
	case model.TagScopeLocation:
		rows, err := r.q.LocationTagUsage(ctx)
		if err != nil {
			return nil, fmt.Errorf("location tag usage: %w", err)
		}
		for _, row := range rows {
			out = append(out, model.TagRow{TagID: row.ID, Name: row.Name, Count: row.Count})
		}
	default:
		return nil, fmt.Errorf("%w: %q", model.ErrInvalidScope, scope)
	}
	return out, nil
}

// CustomersBySource returns customer counts per local month and source, only where there are customers.
func (r *DashboardRepository) CustomersBySource(ctx context.Context, q model.MonthlyQuery) ([]model.SourceMonthCount, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.q.CustomersBySource(ctx, db.CustomersBySourceParams{StartAt: q.Start, EndAt: q.End, Tz: q.TZ})
	if err != nil {
		return nil, fmt.Errorf("customers by source: %w", err)
	}
	out := make([]model.SourceMonthCount, len(rows))
	for i, row := range rows {
		out[i] = model.SourceMonthCount{Month: row.Month, Source: row.Source, Count: row.Count}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nonNil turns a nil slice into an empty one: pgx sends nil as SQL NULL,
// and cardinality(NULL) is NULL rather than 0, which would filter out every row.
func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
