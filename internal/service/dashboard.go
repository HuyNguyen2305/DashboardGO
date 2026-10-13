// Package service holds business logic. It depends on repository interfaces, never on Gin.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/model"
)

// DashboardRepository reads the aggregates behind the dashboard widgets.
type DashboardRepository interface {
	Stats(ctx context.Context, q model.StatsQuery) (model.DashboardStats, error)
	JobStatusBreakdown(ctx context.Context, q model.JobsQuery) ([]model.BreakdownRow, error)
	JobDailyCounts(ctx context.Context, q model.JobsQuery) ([]model.DayStatusCount, error)
	DocumentStatusBreakdown(ctx context.Context, q model.DocumentsQuery) ([]model.BreakdownRow, error)
	InvoiceAging(ctx context.Context, q model.AgingQuery) ([]model.BreakdownRow, error)
	MonthlyRevenue(ctx context.Context, q model.MonthlyQuery) ([]model.MonthlyAmount, error)
	MonthlyForecast(ctx context.Context, q model.MonthlyQuery) ([]model.MonthlyAmount, error)
	PaymentsByMethod(ctx context.Context, q model.PeriodQuery) ([]model.BreakdownRow, error)
	RevenueByStaff(ctx context.Context, q model.StatsQuery) ([]model.StaffRevenueRow, error)
	ActiveServices(ctx context.Context, q model.ServicesQuery) ([]model.ServiceRow, error)
	TagUsage(ctx context.Context, scope string) ([]model.TagRow, error)
	CustomersBySource(ctx context.Context, q model.MonthlyQuery) ([]model.SourceMonthCount, error)
}

// Labels for rows that don't belong to a staff member or service.
const (
	OtherStaffLabel = "Other"
	NoServiceLabel  = "No service"
)

// DashboardService computes dashboard widgets.
type DashboardService struct {
	repo DashboardRepository
}

// NewDashboardService builds a DashboardService on top of repo.
func NewDashboardService(repo DashboardRepository) *DashboardService {
	return &DashboardService{repo: repo}
}

// Stats returns the top Stats row for r, applying the status rules from the model package.
func (s *DashboardService) Stats(ctx context.Context, r daterange.Range) (model.DashboardStats, error) {
	stats, err := s.repo.Stats(ctx, model.StatsQuery{
		Start:                    r.Start,
		End:                      r.End,
		RevenueStatuses:          model.RevenueInvoiceStatuses,
		ForecastEstimateStatuses: model.ForecastEstimateStatuses,
		ForecastInvoiceStatuses:  model.ForecastInvoiceStatuses,
	})
	if err != nil {
		return model.DashboardStats{}, fmt.Errorf("dashboard stats: %w", err)
	}
	return stats, nil
}

// Jobs returns the Jobs widget for r, limited to staffIDs when non-empty.
// Percent is each status's share of the job count.
func (s *DashboardService) Jobs(ctx context.Context, r daterange.Range, staffIDs []int64) (model.JobsWidget, error) {
	q := model.JobsQuery{Start: r.Start, End: r.End, TZ: tzName(r.Start), StaffIDs: staffIDs}

	rows, err := s.repo.JobStatusBreakdown(ctx, q)
	if err != nil {
		return model.JobsWidget{}, fmt.Errorf("jobs widget: %w", err)
	}
	daily, err := s.repo.JobDailyCounts(ctx, q)
	if err != nil {
		return model.JobsWidget{}, fmt.Errorf("jobs widget: %w", err)
	}
	return model.JobsWidget{
		Breakdown: fillBreakdown(model.JobStatuses, rows, byCount),
		Daily:     fillDaily(r, daily),
	}, nil
}

// Invoices returns the Invoices widget for r. Percent is each status's share of the amount.
func (s *DashboardService) Invoices(ctx context.Context, r daterange.Range) (model.Breakdown, error) {
	return s.documents(ctx, r, model.DocTypeInvoice, model.InvoiceStatuses)
}

// Estimates returns the Estimates widget for r. Percent is each status's share of the amount.
func (s *DashboardService) Estimates(ctx context.Context, r daterange.Range) (model.Breakdown, error) {
	return s.documents(ctx, r, model.DocTypeEstimate, model.EstimateStatuses)
}

func (s *DashboardService) documents(ctx context.Context, r daterange.Range, docType string, order []string) (model.Breakdown, error) {
	rows, err := s.repo.DocumentStatusBreakdown(ctx, model.DocumentsQuery{DocType: docType, Start: r.Start, End: r.End})
	if err != nil {
		return model.Breakdown{}, fmt.Errorf("%s widget: %w", docType, err)
	}
	return fillBreakdown(order, rows, byAmount), nil
}

// InvoiceAging returns outstanding balances by age as of asOf, a local midnight in the business timezone.
func (s *DashboardService) InvoiceAging(ctx context.Context, asOf time.Time) (model.AgingWidget, error) {
	rows, err := s.repo.InvoiceAging(ctx, model.AgingQuery{
		AsOf: asOf, TZ: tzName(asOf), Statuses: model.AgingInvoiceStatuses,
	})
	if err != nil {
		return model.AgingWidget{}, fmt.Errorf("invoice aging widget: %w", err)
	}
	return model.AgingWidget{Breakdown: fillBreakdown(model.AgingBuckets, rows, byAmount), AsOf: asOf}, nil
}

// RevenuePerMonth returns revenue for every month in r (normally one year).
func (s *DashboardService) RevenuePerMonth(ctx context.Context, r daterange.Range) (model.YearSeries, error) {
	months, err := s.repo.MonthlyRevenue(ctx, s.monthlyQuery(r))
	if err != nil {
		return model.YearSeries{}, fmt.Errorf("revenue per month widget: %w", err)
	}
	return fillMonths(r, months), nil
}

// SalesForecast returns the forecast for every month in r (normally one year).
func (s *DashboardService) SalesForecast(ctx context.Context, r daterange.Range) (model.YearSeries, error) {
	months, err := s.repo.MonthlyForecast(ctx, s.monthlyQuery(r))
	if err != nil {
		return model.YearSeries{}, fmt.Errorf("sales forecast widget: %w", err)
	}
	return fillMonths(r, months), nil
}

func (s *DashboardService) monthlyQuery(r daterange.Range) model.MonthlyQuery {
	return model.MonthlyQuery{
		Start:                    r.Start,
		End:                      r.End,
		TZ:                       tzName(r.Start),
		RevenueStatuses:          model.RevenueInvoiceStatuses,
		ForecastEstimateStatuses: model.ForecastEstimateStatuses,
		ForecastInvoiceStatuses:  model.ForecastInvoiceStatuses,
	}
}

// Payments returns the Payments widget for r: methods with payments, biggest amount first.
func (s *DashboardService) Payments(ctx context.Context, r daterange.Range) (model.Breakdown, error) {
	rows, err := s.repo.PaymentsByMethod(ctx, model.PeriodQuery{Start: r.Start, End: r.End})
	if err != nil {
		return model.Breakdown{}, fmt.Errorf("payments widget: %w", err)
	}
	return fillBreakdown(nil, rows, byAmount), nil
}

// RevenueByStaff returns revenue per staff member for r, biggest first. Invoices with no job,
// or whose job has no staff, form the "Other" row. Percent is each row's share of the amount.
func (s *DashboardService) RevenueByStaff(ctx context.Context, r daterange.Range) (model.StaffRevenue, error) {
	rows, err := s.repo.RevenueByStaff(ctx, model.StatsQuery{
		Start: r.Start, End: r.End, RevenueStatuses: model.RevenueInvoiceStatuses,
	})
	if err != nil {
		return model.StaffRevenue{}, fmt.Errorf("revenue by staff widget: %w", err)
	}

	w := model.StaffRevenue{Rows: make([]model.StaffRevenueRow, len(rows)), TotalAmount: decimal.Zero}
	for _, row := range rows {
		w.TotalAmount = w.TotalAmount.Add(row.Amount)
	}
	for i, row := range rows {
		if row.StaffID == nil {
			row.Name = OtherStaffLabel
		}
		row.Percent = percent(row.Amount, w.TotalAmount)
		w.Rows[i] = row
	}
	return w, nil
}

// ActiveServices returns incomplete jobs in r grouped by service, biggest first.
// Percent is each service's share of the amount.
func (s *DashboardService) ActiveServices(ctx context.Context, r daterange.Range) (model.ActiveServices, error) {
	rows, err := s.repo.ActiveServices(ctx, model.ServicesQuery{
		Start: r.Start, End: r.End, Statuses: model.ActiveJobStatuses,
	})
	if err != nil {
		return model.ActiveServices{}, fmt.Errorf("active services widget: %w", err)
	}

	w := model.ActiveServices{Rows: make([]model.ServiceRow, len(rows)), TotalAmount: decimal.Zero}
	for _, row := range rows {
		w.TotalCount += row.Count
		w.TotalAmount = w.TotalAmount.Add(row.Amount)
		if row.IsRecurring {
			w.RecurringCount += row.Count
		}
	}
	for i, row := range rows {
		if row.ServiceID == nil {
			row.Name = NoServiceLabel
		}
		row.Percent = percent(row.Amount, w.TotalAmount)
		w.Rows[i] = row
	}
	return w, nil
}

// Tags returns the tags in use for scope (location or customer), most used first.
// Percent is each tag's share of all tag uses in the scope.
func (s *DashboardService) Tags(ctx context.Context, scope string) (model.TagUsage, error) {
	if scope != model.TagScopeLocation && scope != model.TagScopeCustomer {
		return model.TagUsage{}, fmt.Errorf("%w: %q, must be %s or %s",
			model.ErrInvalidScope, scope, model.TagScopeLocation, model.TagScopeCustomer)
	}
	rows, err := s.repo.TagUsage(ctx, scope)
	if err != nil {
		return model.TagUsage{}, fmt.Errorf("tags widget: %w", err)
	}

	w := model.TagUsage{Scope: scope, Rows: make([]model.TagRow, len(rows))}
	for _, row := range rows {
		w.TotalCount += row.Count
	}
	for i, row := range rows {
		row.Percent = percent(decimal.NewFromInt(row.Count), decimal.NewFromInt(w.TotalCount))
		w.Rows[i] = row
	}
	return w, nil
}

// CustomersBySource returns new customers and leads per month in r (normally one year) for every source.
func (s *DashboardService) CustomersBySource(ctx context.Context, r daterange.Range) (model.SourceSeries, error) {
	counts, err := s.repo.CustomersBySource(ctx, model.MonthlyQuery{Start: r.Start, End: r.End, TZ: tzName(r.Start)})
	if err != nil {
		return model.SourceSeries{}, fmt.Errorf("customers by source widget: %w", err)
	}
	return fillSourceMonths(r, counts), nil
}

type percentBasis int

const (
	byCount percentBasis = iota
	byAmount
)

// fillBreakdown lists every key in order (zero rows for missing ones), then any
// unexpected keys in the order the repository returned them, so no data is dropped.
// With a nil order, rows keep the repository's order.
func fillBreakdown(order []string, rows []model.BreakdownRow, basis percentBasis) model.Breakdown {
	byKey := make(map[string]model.BreakdownRow, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}

	out := make([]model.BreakdownRow, 0, len(order)+len(rows))
	for _, key := range order {
		row := byKey[key]
		row.Key = key
		out = append(out, row)
		delete(byKey, key)
	}
	for _, row := range rows {
		if _, extra := byKey[row.Key]; extra {
			out = append(out, row)
		}
	}

	b := model.Breakdown{Rows: out, TotalAmount: decimal.Zero}
	for _, row := range out {
		b.TotalCount += row.Count
		b.TotalAmount = b.TotalAmount.Add(row.Amount)
	}
	for i := range b.Rows {
		if basis == byCount {
			b.Rows[i].Percent = percent(decimal.NewFromInt(b.Rows[i].Count), decimal.NewFromInt(b.TotalCount))
		} else {
			b.Rows[i].Percent = percent(b.Rows[i].Amount, b.TotalAmount)
		}
	}
	return b
}

// percent returns part/whole as a 0–100 value rounded to 2 decimals, or 0 when whole is 0.
func percent(part, whole decimal.Decimal) float64 {
	if whole.IsZero() {
		return 0
	}
	return part.Div(whole).Mul(decimal.NewFromInt(100)).Round(2).InexactFloat64()
}

// fillDaily returns one entry per day in r, with a count for every job status.
func fillDaily(r daterange.Range, counts []model.DayStatusCount) []model.DailyCounts {
	const layout = "2006-01-02"
	byDay := make(map[string][]model.DayStatusCount)
	for _, c := range counts {
		key := c.Day.Format(layout)
		byDay[key] = append(byDay[key], c)
	}

	days := r.Days()
	out := make([]model.DailyCounts, len(days))
	for i, day := range days {
		m := make(map[string]int64, len(model.JobStatuses))
		for _, status := range model.JobStatuses {
			m[status] = 0
		}
		for _, c := range byDay[day.Format(layout)] {
			m[c.Status] += c.Count
		}
		out[i] = model.DailyCounts{Date: day, Counts: m}
	}
	return out
}

// fillMonths returns one entry per month in r with zero for months without data, plus the total.
func fillMonths(r daterange.Range, months []model.MonthlyAmount) model.YearSeries {
	const layout = "2006-01"
	byMonth := make(map[string]decimal.Decimal, len(months))
	for _, m := range months {
		byMonth[m.Month.Format(layout)] = m.Amount
	}

	starts := r.Months()
	series := model.YearSeries{Months: make([]model.MonthlyAmount, len(starts)), Total: decimal.Zero}
	for i, start := range starts {
		amount, ok := byMonth[start.Format(layout)]
		if !ok {
			amount = decimal.Zero
		}
		series.Months[i] = model.MonthlyAmount{Month: start, Amount: amount}
		series.Total = series.Total.Add(amount)
	}
	return series
}

// fillSourceMonths returns one entry per month in r with a count for every source.
// Sources the dashboard doesn't know yet are appended to Sources so no customer is dropped.
func fillSourceMonths(r daterange.Range, counts []model.SourceMonthCount) model.SourceSeries {
	const layout = "2006-01"
	sources := append([]string(nil), model.CustomerSources...)
	known := make(map[string]bool, len(sources))
	for _, src := range sources {
		known[src] = true
	}
	byMonth := make(map[string][]model.SourceMonthCount)
	for _, c := range counts {
		if !known[c.Source] {
			known[c.Source] = true
			sources = append(sources, c.Source)
		}
		key := c.Month.Format(layout)
		byMonth[key] = append(byMonth[key], c)
	}

	starts := r.Months()
	series := model.SourceSeries{Sources: sources, Months: make([]model.MonthlySourceCounts, len(starts))}
	for i, start := range starts {
		m := make(map[string]int64, len(sources))
		for _, src := range sources {
			m[src] = 0
		}
		for _, c := range byMonth[start.Format(layout)] {
			m[c.Source] += c.Count
			series.TotalCount += c.Count
		}
		series.Months[i] = model.MonthlySourceCounts{Month: start, Counts: m}
	}
	return series
}

// tzName is the IANA name Postgres needs to group timestamps by local day or month.
func tzName(t time.Time) string {
	return t.Location().String()
}
