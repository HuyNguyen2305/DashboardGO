package dto

import "github.com/shopspring/decimal"

// Money formats an amount for JSON as a string with exactly 2 decimals, e.g. "1487.40".
// Strings keep amounts exact; JSON numbers would be parsed as floats by clients.
func Money(d decimal.Decimal) string {
	return d.StringFixed(2)
}

// StatsResponse is the top Stats row of the dashboard.
type StatsResponse struct {
	Range               DateRange `json:"range"`
	Revenue             string    `json:"revenue"`
	Collected           string    `json:"collected"`
	JobsCompleted       int64     `json:"jobs_completed"`
	NewJobsBookedOnline string    `json:"new_jobs_booked_online"`
	TotalNewJobs        string    `json:"total_new_jobs"`
}

// BreakdownRow is one line of a breakdown widget. Percent is 0–100.
type BreakdownRow struct {
	Key     string  `json:"key"`
	Count   int64   `json:"count"`
	Amount  string  `json:"amount"`
	Percent float64 `json:"percent"`
}

// BreakdownResponse serves the Invoices, Estimates and Payments widgets.
type BreakdownResponse struct {
	Range       DateRange      `json:"range"`
	TotalAmount string         `json:"total_amount"`
	TotalCount  int64          `json:"total_count"`
	Rows        []BreakdownRow `json:"rows"`
}

// DailyCounts is one point of the Jobs line chart: job counts per status on Date (YYYY-MM-DD).
type DailyCounts struct {
	Date   string           `json:"date"`
	Counts map[string]int64 `json:"counts"`
}

// JobsResponse is the Jobs widget: the breakdown fields plus the daily chart.
type JobsResponse struct {
	BreakdownResponse
	Daily []DailyCounts `json:"daily"`
}

// AgingResponse is the Invoices Aging widget as of AsOf (YYYY-MM-DD).
type AgingResponse struct {
	AsOf        string         `json:"as_of"`
	TotalAmount string         `json:"total_amount"`
	TotalCount  int64          `json:"total_count"`
	Rows        []BreakdownRow `json:"rows"`
}

// MonthlyAmount is one point of a yearly chart; Month is YYYY-MM.
type MonthlyAmount struct {
	Month  string `json:"month"`
	Amount string `json:"amount"`
}

// YearSeriesResponse serves the Revenue per Month and Sales Forecast widgets.
type YearSeriesResponse struct {
	Range  DateRange       `json:"range"`
	Total  string          `json:"total"`
	Months []MonthlyAmount `json:"months"`
}

// StaffRevenueRow is one line of Revenue by Staff. A null StaffID is the "Other" row.
type StaffRevenueRow struct {
	StaffID   *int64  `json:"staff_id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url"`
	Count     int64   `json:"count"`
	Amount    string  `json:"amount"`
	Percent   float64 `json:"percent"`
}

// StaffRevenueResponse is the Revenue by Staff widget.
type StaffRevenueResponse struct {
	Range       DateRange         `json:"range"`
	TotalAmount string            `json:"total_amount"`
	Rows        []StaffRevenueRow `json:"rows"`
}

// ServiceRow is one line of Active Services. A null ServiceID is the "No service" row.
type ServiceRow struct {
	ServiceID   *int64  `json:"service_id"`
	Name        string  `json:"name"`
	IsRecurring bool    `json:"is_recurring"`
	Count       int64   `json:"count"`
	Amount      string  `json:"amount"`
	Percent     float64 `json:"percent"`
}

// ActiveServicesResponse is the Active Services widget.
type ActiveServicesResponse struct {
	Range          DateRange    `json:"range"`
	TotalCount     int64        `json:"total_count"`
	RecurringCount int64        `json:"recurring_count"`
	TotalAmount    string       `json:"total_amount"`
	Rows           []ServiceRow `json:"rows"`
}

// TagRow is one tag in use.
type TagRow struct {
	TagID   int64   `json:"tag_id"`
	Name    string  `json:"name"`
	Count   int64   `json:"count"`
	Percent float64 `json:"percent"`
}

// TagsResponse is the Tags widget for one scope.
type TagsResponse struct {
	Scope      string   `json:"scope"`
	TotalCount int64    `json:"total_count"`
	Rows       []TagRow `json:"rows"`
}

// MonthlySourceCounts is one month of the customers-by-source chart; Month is YYYY-MM.
type MonthlySourceCounts struct {
	Month  string           `json:"month"`
	Counts map[string]int64 `json:"counts"`
}

// CustomersBySourceResponse is the New Customers & Leads by Source chart.
type CustomersBySourceResponse struct {
	Range      DateRange             `json:"range"`
	TotalCount int64                 `json:"total_count"`
	Sources    []string              `json:"sources"`
	Months     []MonthlySourceCounts `json:"months"`
}
