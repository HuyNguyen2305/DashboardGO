// Package model holds domain types and business rules shared across layers.
package model

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Status sets that decide which documents count toward dashboard money figures.
var (
	// RevenueInvoiceStatuses are invoices that count as revenue (billed, tax excluded: their subtotal).
	RevenueInvoiceStatuses = []string{"sent", "partial_paid", "paid"}
	// NewJobsEstimateStatuses are estimates counted in Total New Jobs: still expected to turn into work (everything but lost).
	NewJobsEstimateStatuses = []string{"draft", "pending", "won", "invoiced"}
	// NewJobsInvoiceStatuses are invoices counted in Total New Jobs.
	NewJobsInvoiceStatuses = []string{"sent", "partial_paid", "paid"}
	// AgingInvoiceStatuses are invoices sent to the customer and not fully paid.
	// A failed invoice (its payment failed) is still owed, so it ages too.
	AgingInvoiceStatuses = []string{"sent", "partial_paid", "failed"}
	// BookedJobStatuses are jobs still on the books (not canceled or terminated). They count in
	// New Jobs Booked Online and in the Sales Forecast.
	BookedJobStatuses = []string{"incomplete", "completed", "reschedule", "workpool"}
)

// Row order of the breakdown widgets, as shown on the dashboard.
var (
	JobStatuses      = []string{"incomplete", "completed", "reschedule", "canceled", "terminated", "workpool"}
	InvoiceStatuses  = []string{"draft", "void", "sent", "partial_paid", "paid", "write_off", "failed"}
	EstimateStatuses = []string{"draft", "pending", "won", "invoiced", "lost"}
	AgingBuckets     = []string{"current", "1_30", "31_60", "61_90", "90_plus"}
	// PaymentMethods matches the payments.method CHECK. The Payments widget lists them biggest amount first.
	PaymentMethods = []string{"check", "stripe", "cash", "card", "other"}
)

// Document types stored in the documents table.
const (
	DocTypeInvoice  = "invoice"
	DocTypeEstimate = "estimate"
)

// StatsQuery selects the data behind the top Stats row.
type StatsQuery struct {
	Start, End              time.Time
	RevenueStatuses         []string
	NewJobsEstimateStatuses []string
	NewJobsInvoiceStatuses  []string
	OnlineJobStatuses       []string
}

// DashboardStats is the top Stats row.
type DashboardStats struct {
	Revenue             decimal.Decimal
	Collected           decimal.Decimal
	JobsCompleted       int64
	NewJobsBookedOnline decimal.Decimal
	TotalNewJobs        decimal.Decimal
}

// PeriodQuery selects rows dated within [Start, End).
type PeriodQuery struct {
	Start, End time.Time
}

// TxOptions configures a repository transaction.
type TxOptions struct {
	// ReadOnly rejects writes.
	ReadOnly bool
	// Snapshot makes every query see the database as of the first one (REPEATABLE READ).
	Snapshot bool
}

// StaffFilter is the Schedules filter of the Jobs widget. IDs selects those staff members and
// Unassigned selects jobs with no staff; the zero value means all schedules.
type StaffFilter struct {
	IDs        []int64
	Unassigned bool
}

// JobsQuery selects jobs scheduled within [Start, End), limited by Staff.
// TZ is the IANA zone used to group by local day.
type JobsQuery struct {
	Start, End time.Time
	TZ         string
	Staff      StaffFilter
}

// DocumentsQuery selects documents of one type issued within [Start, End).
type DocumentsQuery struct {
	DocType    string
	Start, End time.Time
}

// AgingQuery selects outstanding invoices, aged as of the local date AsOf.
type AgingQuery struct {
	AsOf     time.Time
	TZ       string
	Statuses []string
}

// MonthlyQuery selects per-month totals within [Start, End), grouped by local month in TZ.
// RevenueStatuses filters invoices (Revenue per Month); JobStatuses filters jobs (Sales Forecast).
type MonthlyQuery struct {
	Start, End      time.Time
	TZ              string
	RevenueStatuses []string
	JobStatuses     []string
}

// ServicesQuery selects jobs in Statuses scheduled within [Start, End).
type ServicesQuery struct {
	Start, End time.Time
	Statuses   []string
}

// BreakdownRow is one line of a breakdown widget. Percent is 0–100 with 2 decimals.
type BreakdownRow struct {
	Key     string
	Count   int64
	Amount  decimal.Decimal
	Percent float64
}

// Breakdown is a widget made of rows plus header totals.
type Breakdown struct {
	Rows        []BreakdownRow
	TotalCount  int64
	TotalAmount decimal.Decimal
}

// DayStatusCount is the number of jobs with Status scheduled on local day Day.
type DayStatusCount struct {
	Day    time.Time
	Status string
	Count  int64
}

// DailyCounts holds job counts per status for one local day.
type DailyCounts struct {
	Date   time.Time
	Counts map[string]int64
}

// JobsWidget is the Jobs widget: status rows plus the daily line chart.
type JobsWidget struct {
	Breakdown
	Daily []DailyCounts
}

// AgingWidget is the Invoices Aging widget as of local date AsOf.
type AgingWidget struct {
	Breakdown
	AsOf time.Time
}

// MonthlyAmount is a money total for the local month starting at Month.
type MonthlyAmount struct {
	Month  time.Time
	Amount decimal.Decimal
}

// YearSeries is a per-month chart plus its total.
type YearSeries struct {
	Months []MonthlyAmount
	Total  decimal.Decimal
}

// ActiveJobStatuses are the job statuses counted by the Active Services widget.
var ActiveJobStatuses = []string{"incomplete"}

// CustomerSources is the legend order of the customers-by-source chart. "na" is a customer with no source.
var CustomerSources = []string{"yelp", "angieslist", "craigslist", "facebook", "google", "referral", "na"}

// Tag scopes for the Tags widget.
const (
	TagScopeLocation = "location"
	TagScopeCustomer = "customer"
)

// ErrInvalidScope is returned for a tag scope other than location or customer.
var ErrInvalidScope = errors.New("invalid tag scope")

// StaffRevenueRow is one line of Revenue by Staff. A nil StaffID is the "Other" row.
type StaffRevenueRow struct {
	StaffID   *int64
	Name      string
	AvatarURL *string
	Count     int64
	Amount    decimal.Decimal
	Percent   float64
}

// StaffRevenue is the Revenue by Staff widget.
type StaffRevenue struct {
	Rows        []StaffRevenueRow
	TotalAmount decimal.Decimal
}

// ServiceRow is one line of Active Services. A nil ServiceID is the "No service" row.
type ServiceRow struct {
	ServiceID     *int64
	Name          string
	IsRecurring   bool
	Count         int64 // visits (jobs)
	CustomerCount int64 // distinct customers
	Amount        decimal.Decimal
	Percent       float64
}

// ServiceCustomers counts the distinct customers behind the Active Services widget.
type ServiceCustomers struct {
	CustomerCount          int64
	RecurringCustomerCount int64
}

// ActiveServices is the Active Services widget.
// TotalCount and RecurringCount are visits; the ServiceCustomers fields are distinct customers.
type ActiveServices struct {
	Rows           []ServiceRow
	TotalCount     int64
	RecurringCount int64
	TotalAmount    decimal.Decimal
	ServiceCustomers
}

// MaxTagRows caps the Tags widget; tags past it are folded into one "Other tags" row.
const MaxTagRows = 50

// TagRow is one tag in use and how many customers or locations carry it. A nil TagID is the
// "Other tags" row. CustomerCount is set for location tags only: the distinct customers owning
// the tagged locations (not for "Other tags", where distinct counts can't be added up).
type TagRow struct {
	TagID         *int64
	Name          string
	Count         int64
	CustomerCount *int64
	Percent       float64
}

// TagUsage is the Tags widget for one scope.
type TagUsage struct {
	Scope      string
	Rows       []TagRow
	TotalCount int64
}

// SourceMonthCount is the number of customers from Source created in the local month starting at Month.
type SourceMonthCount struct {
	Month  time.Time
	Source string
	Count  int64
}

// MonthlySourceCounts holds customer counts per source for one local month.
type MonthlySourceCounts struct {
	Month  time.Time
	Counts map[string]int64
}

// SourceSeries is the New Customers & Leads by Source chart.
type SourceSeries struct {
	Sources    []string
	Months     []MonthlySourceCounts
	TotalCount int64
}
