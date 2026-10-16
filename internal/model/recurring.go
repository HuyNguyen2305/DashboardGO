package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"dashboardgo/internal/recurrence"
)

// Recurring plan statuses.
const (
	PlanStatusActive   = "active"
	PlanStatusPaused   = "paused"
	PlanStatusCanceled = "canceled"
)

// PlanAction is a status change requested on a recurring plan.
type PlanAction string

// Supported plan actions.
const (
	PlanPause  PlanAction = "pause"
	PlanResume PlanAction = "resume"
	PlanCancel PlanAction = "cancel"
)

// Domain errors for recurring plans; the handler maps them to HTTP codes.
var (
	ErrPlanNotFound      = errors.New("recurring plan not found")
	ErrCustomerNotFound  = errors.New("customer not found")
	ErrPlanCanceled      = errors.New("recurring plan is canceled")
	ErrInvalidTransition = errors.New("invalid plan status transition")
	ErrCadenceLocked     = errors.New("plan cadence is locked by upcoming linked jobs")
	ErrInvalidReference  = errors.New("invalid reference")
	ErrWindowTooLarge    = errors.New("window too large")
	// ErrInvalidPlan means a plan field is out of range for storage.
	ErrInvalidPlan = errors.New("invalid recurring plan")
)

// RecurringPlan is a repeating service for a customer.
type RecurringPlan struct {
	ID, CustomerID, ServiceID int64
	LocationID, StaffID       *int64
	ServiceName               string
	Status                    string // PlanStatusActive | PlanStatusPaused | PlanStatusCanceled
	Rule                      recurrence.Rule
	Quantity, UnitPrice       decimal.Decimal
	TaxRate                   decimal.Decimal
	PaymentTermsDays          int
	CreatedAt, UpdatedAt      time.Time
}

// ProjectedLine is the single line of a projected invoice or estimate.
type ProjectedLine struct {
	Description         string
	Quantity, UnitPrice decimal.Decimal
	Amount              decimal.Decimal
}

// ProjectedInvoice is the invoice a visit would produce, computed on read.
type ProjectedInvoice struct {
	PlanID               int64
	Occurrence           recurrence.Occurrence
	Ref                  string    // "PRJ-<planID>-<index>"; never a real documents.number
	IssuedOn, DueOn      time.Time // visit date; visit date + PaymentTermsDays
	Line                 ProjectedLine
	Subtotal, Tax, Total decimal.Decimal
}

// ProjectedEstimate is the estimate covering every visit of a plan in a window.
type ProjectedEstimate struct {
	PlanID     int64
	From, To   time.Time // half-open period actually covered
	VisitCount int
	// Quantity = VisitCount × plan.Quantity. Amount (= Subtotal) sums the per-visit rounded
	// invoices, so it can differ from Quantity × UnitPrice by cents.
	Line                 ProjectedLine
	Subtotal, Tax, Total decimal.Decimal
}

// Optional is a patch field: Set tells "present in the request" apart from the zero Value.
type Optional[T any] struct {
	Set   bool
	Value T
}

// NewRecurringPlan is the input to create a plan.
type NewRecurringPlan struct {
	CustomerID, ServiceID int64
	LocationID, StaffID   *int64
	Rule                  recurrence.Rule
	Quantity              decimal.Decimal
	UnitPrice             *decimal.Decimal // nil = the service price
	TaxRate               decimal.Decimal
	PaymentTermsDays      int
}

// RecurringPlanPatch is a partial update; only fields with Set change.
type RecurringPlanPatch struct {
	LocationID, StaffID Optional[*int64]
	EndDate             Optional[*time.Time]
	IntervalUnit        Optional[recurrence.Unit]
	IntervalCount       Optional[int]
	StartDate           Optional[time.Time]
	Quantity            Optional[decimal.Decimal]
	UnitPrice           Optional[decimal.Decimal]
	TaxRate             Optional[decimal.Decimal]
	PaymentTermsDays    Optional[int]
}

// ChangesCadence reports whether the patch moves the visit dates of current: a Set unit, count
// or start counts only when it differs from current (start compared by calendar date), so a
// client re-sending unchanged values does not trip the cadence lock.
func (p RecurringPlanPatch) ChangesCadence(current recurrence.Rule) bool {
	return (p.IntervalUnit.Set && p.IntervalUnit.Value != current.Unit) ||
		(p.IntervalCount.Set && p.IntervalCount.Value != current.Count) ||
		(p.StartDate.Set && recurrence.DateKey(p.StartDate.Value) != recurrence.DateKey(current.Start))
}

// IsEmpty reports whether the patch sets no field at all.
func (p RecurringPlanPatch) IsEmpty() bool {
	return !p.LocationID.Set && !p.StaffID.Set && !p.EndDate.Set && !p.IntervalUnit.Set &&
		!p.IntervalCount.Set && !p.StartDate.Set && !p.Quantity.Set && !p.UnitPrice.Set &&
		!p.TaxRate.Set && !p.PaymentTermsDays.Set
}

// Apply returns plan with every Set field of the patch applied.
func (p RecurringPlanPatch) Apply(plan RecurringPlan) RecurringPlan {
	if p.LocationID.Set {
		plan.LocationID = p.LocationID.Value
	}
	if p.StaffID.Set {
		plan.StaffID = p.StaffID.Value
	}
	if p.EndDate.Set {
		plan.Rule.End = p.EndDate.Value
	}
	if p.IntervalUnit.Set {
		plan.Rule.Unit = p.IntervalUnit.Value
	}
	if p.IntervalCount.Set {
		plan.Rule.Count = p.IntervalCount.Value
	}
	if p.StartDate.Set {
		plan.Rule.Start = p.StartDate.Value
	}
	if p.Quantity.Set {
		plan.Quantity = p.Quantity.Value
	}
	if p.UnitPrice.Set {
		plan.UnitPrice = p.UnitPrice.Value
	}
	if p.TaxRate.Set {
		plan.TaxRate = p.TaxRate.Value
	}
	if p.PaymentTermsDays.Set {
		plan.PaymentTermsDays = p.PaymentTermsDays.Value
	}
	return plan
}

// PlanListQuery filters and pages the plan list (keyset on ID).
type PlanListQuery struct {
	CustomerID *int64
	Status     *string
	After      int64
	Limit      int
}

// PlanPage is one page of plans; NextCursor is nil on the last page.
type PlanPage struct {
	Items      []RecurringPlan
	NextCursor *int64
}

// Window is a half-open date range [From, To).
type Window struct{ From, To time.Time }

// PlanOccurrence is a visit of a plan, with its linked job if one exists.
type PlanOccurrence struct {
	PlanID int64
	recurrence.Occurrence
	JobID *int64
}

// LinkedVisits maps planID → recurrence.DateKey → jobID.
type LinkedVisits map[int64]map[string]int64

// NextPlanStatus returns the status after applying a to current. Pause and
// resume are idempotent; a canceled plan only accepts cancel.
func NextPlanStatus(current string, a PlanAction) (string, error) {
	switch a {
	case PlanPause, PlanResume, PlanCancel:
	default:
		return "", fmt.Errorf("action %q: %w", a, ErrInvalidTransition)
	}
	if current == PlanStatusCanceled {
		if a == PlanCancel {
			return PlanStatusCanceled, nil
		}
		return "", fmt.Errorf("%s a canceled plan: %w", a, ErrInvalidTransition)
	}
	switch a {
	case PlanPause:
		return PlanStatusPaused, nil
	case PlanResume:
		return PlanStatusActive, nil
	default:
		return PlanStatusCanceled, nil
	}
}

// ProjectInvoice computes the invoice for one visit. Amounts round half away
// from zero to cents, like Postgres round(numeric, 2).
func ProjectInvoice(p RecurringPlan, o recurrence.Occurrence) ProjectedInvoice {
	amount := p.Quantity.Mul(p.UnitPrice).Round(2)
	tax := amount.Mul(p.TaxRate).Round(2)
	y, m, d := o.Date.Date()
	return ProjectedInvoice{
		PlanID:     p.ID,
		Occurrence: o,
		Ref:        fmt.Sprintf("PRJ-%d-%d", p.ID, o.Index),
		IssuedOn:   o.Date,
		DueOn:      time.Date(y, m, d+p.PaymentTermsDays, 0, 0, 0, 0, o.Date.Location()),
		Line: ProjectedLine{
			Description: p.ServiceName,
			Quantity:    p.Quantity,
			UnitPrice:   p.UnitPrice,
			Amount:      amount,
		},
		Subtotal: amount,
		Tax:      tax,
		Total:    amount.Add(tax),
	}
}

// ProjectEstimate sums the per-visit invoices of occs, so an estimate always
// equals the invoices for the same visits to the cent.
func ProjectEstimate(p RecurringPlan, occs []recurrence.Occurrence, w Window) ProjectedEstimate {
	subtotal, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
	for _, o := range occs {
		inv := ProjectInvoice(p, o)
		subtotal = subtotal.Add(inv.Subtotal)
		tax = tax.Add(inv.Tax)
		total = total.Add(inv.Total)
	}
	return ProjectedEstimate{
		PlanID:     p.ID,
		From:       w.From,
		To:         w.To,
		VisitCount: len(occs),
		Line: ProjectedLine{
			Description: p.ServiceName,
			Quantity:    p.Quantity.Mul(decimal.NewFromInt(int64(len(occs)))),
			UnitPrice:   p.UnitPrice,
			Amount:      subtotal,
		},
		Subtotal: subtotal,
		Tax:      tax,
		Total:    total,
	}
}
