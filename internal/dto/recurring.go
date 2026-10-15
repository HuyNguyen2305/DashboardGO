package dto

import (
	"encoding/json"
	"fmt"
	"time"

	"dashboardgo/internal/model"
)

// Optional is a PATCH field. Set is true when the key is present in the body, even as JSON null,
// so "leave unchanged" and "clear" can be told apart. Null is true for an explicit JSON null.
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON marks the field as present and decodes its value.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	o.Null = string(b) == "null"
	if err := json.Unmarshal(b, &o.Value); err != nil {
		return fmt.Errorf("decode optional: %w", err)
	}
	return nil
}

// CreateRecurringPlanRequest is the body of POST /api/v1/recurring-plans.
// Money and rates are decimal strings; the handler parses and range-checks them.
type CreateRecurringPlanRequest struct {
	CustomerID       int64   `json:"customer_id"        binding:"required,min=1"`
	LocationID       *int64  `json:"location_id"        binding:"omitempty,min=1"`
	ServiceID        int64   `json:"service_id"         binding:"required,min=1"`
	StaffID          *int64  `json:"staff_id"           binding:"omitempty,min=1"`
	IntervalUnit     string  `json:"interval_unit"      binding:"required,oneof=day week month"`
	IntervalCount    int     `json:"interval_count"     binding:"required,min=1,max=365"`
	StartDate        string  `json:"start_date"         binding:"required,datetime=2006-01-02"`
	EndDate          *string `json:"end_date"           binding:"omitempty,datetime=2006-01-02"`
	Quantity         *string `json:"quantity"`
	UnitPrice        *string `json:"unit_price"`
	TaxRate          *string `json:"tax_rate"`
	PaymentTermsDays *int    `json:"payment_terms_days" binding:"omitempty,min=0,max=365"`
}

// UpdateRecurringPlanRequest is the body of PATCH /api/v1/recurring-plans/:id.
// customer_id, service_id and status are not fields here, so the strict decoder rejects them.
type UpdateRecurringPlanRequest struct {
	LocationID       Optional[*int64]  `json:"location_id"`
	StaffID          Optional[*int64]  `json:"staff_id"`
	EndDate          Optional[*string] `json:"end_date"`
	IntervalUnit     Optional[string]  `json:"interval_unit"`
	IntervalCount    Optional[int]     `json:"interval_count"`
	StartDate        Optional[string]  `json:"start_date"`
	Quantity         Optional[string]  `json:"quantity"`
	UnitPrice        Optional[string]  `json:"unit_price"`
	TaxRate          Optional[string]  `json:"tax_rate"`
	PaymentTermsDays Optional[int]     `json:"payment_terms_days"`
}

// RecurringPlanResponse is one plan.
type RecurringPlanResponse struct {
	ID               int64   `json:"id"`
	CustomerID       int64   `json:"customer_id"`
	LocationID       *int64  `json:"location_id"`
	ServiceID        int64   `json:"service_id"`
	ServiceName      string  `json:"service_name"`
	StaffID          *int64  `json:"staff_id"`
	Status           string  `json:"status"`
	IntervalUnit     string  `json:"interval_unit"`
	IntervalCount    int     `json:"interval_count"`
	StartDate        string  `json:"start_date"`
	EndDate          *string `json:"end_date"`
	Quantity         string  `json:"quantity"`
	UnitPrice        string  `json:"unit_price"`
	TaxRate          string  `json:"tax_rate"`
	PaymentTermsDays int     `json:"payment_terms_days"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

// RecurringPlanPage is one keyset page of plans; NextCursor is null on the last page.
type RecurringPlanPage struct {
	Items      []RecurringPlanResponse `json:"items"`
	NextCursor *int64                  `json:"next_cursor"`
}

const dateLayout = "2006-01-02"

// NewRecurringPlanResponse maps a plan to its JSON shape.
func NewRecurringPlanResponse(p model.RecurringPlan) RecurringPlanResponse {
	var end *string
	if p.Rule.End != nil {
		s := p.Rule.End.Format(dateLayout)
		end = &s
	}
	return RecurringPlanResponse{
		ID:               p.ID,
		CustomerID:       p.CustomerID,
		LocationID:       p.LocationID,
		ServiceID:        p.ServiceID,
		ServiceName:      p.ServiceName,
		StaffID:          p.StaffID,
		Status:           p.Status,
		IntervalUnit:     string(p.Rule.Unit),
		IntervalCount:    p.Rule.Count,
		StartDate:        p.Rule.Start.Format(dateLayout),
		EndDate:          end,
		Quantity:         Money(p.Quantity),
		UnitPrice:        Money(p.UnitPrice),
		TaxRate:          p.TaxRate.StringFixed(4),
		PaymentTermsDays: p.PaymentTermsDays,
		CreatedAt:        p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        p.UpdatedAt.Format(time.RFC3339),
	}
}

// NewRecurringPlanPage maps a page of plans; Items is never null.
func NewRecurringPlanPage(pg model.PlanPage) RecurringPlanPage {
	items := make([]RecurringPlanResponse, len(pg.Items))
	for i, p := range pg.Items {
		items[i] = NewRecurringPlanResponse(p)
	}
	return RecurringPlanPage{Items: items, NextCursor: pg.NextCursor}
}

// Fields maps each PATCH key to the field it fills, so the handler can name the key of a bad value.
func (r *UpdateRecurringPlanRequest) Fields() map[string]json.Unmarshaler {
	return map[string]json.Unmarshaler{
		"location_id":        &r.LocationID,
		"staff_id":           &r.StaffID,
		"end_date":           &r.EndDate,
		"interval_unit":      &r.IntervalUnit,
		"interval_count":     &r.IntervalCount,
		"start_date":         &r.StartDate,
		"quantity":           &r.Quantity,
		"unit_price":         &r.UnitPrice,
		"tax_rate":           &r.TaxRate,
		"payment_terms_days": &r.PaymentTermsDays,
	}
}

// OccurrenceResponse is one visit of a plan; JobID is null while the visit is unbooked.
type OccurrenceResponse struct {
	PlanID int64  `json:"plan_id"`
	Index  int    `json:"index"`
	Date   string `json:"date"`
	JobID  *int64 `json:"job_id"`
}

// OccurrenceList is the occurrences in a window.
type OccurrenceList struct {
	Range DateRange            `json:"range"`
	Items []OccurrenceResponse `json:"items"`
}

// ProjectedLineResponse is the single line of a projected invoice or estimate.
type ProjectedLineResponse struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	UnitPrice   string `json:"unit_price"`
	Amount      string `json:"amount"`
}

// ProjectedInvoiceResponse is the invoice one visit would produce.
type ProjectedInvoiceResponse struct {
	PlanID   int64                 `json:"plan_id"`
	Ref      string                `json:"ref"`
	Index    int                   `json:"index"`
	IssuedOn string                `json:"issued_on"`
	DueOn    string                `json:"due_on"`
	Line     ProjectedLineResponse `json:"line"`
	Subtotal string                `json:"subtotal"`
	Tax      string                `json:"tax"`
	Total    string                `json:"total"`
	Kind     string                `json:"kind"`
}

// ProjectedInvoiceList is the projected invoices in a window.
type ProjectedInvoiceList struct {
	Range DateRange                  `json:"range"`
	Items []ProjectedInvoiceResponse `json:"items"`
}

// ProjectedEstimateResponse is the estimate covering every visit in the range.
type ProjectedEstimateResponse struct {
	PlanID     int64                 `json:"plan_id"`
	Range      DateRange             `json:"range"`
	VisitCount int                   `json:"visit_count"`
	Line       ProjectedLineResponse `json:"line"`
	Subtotal   string                `json:"subtotal"`
	Tax        string                `json:"tax"`
	Total      string                `json:"total"`
	Kind       string                `json:"kind"`
}

const projectedKind = "projected"

// CustomRange builds the echoed range for an explicit window; end is the last included day.
func CustomRange(start, end time.Time) DateRange {
	return DateRange{Preset: "custom", Start: start.Format(dateLayout), End: end.Format(dateLayout)}
}

func newProjectedLine(l model.ProjectedLine) ProjectedLineResponse {
	return ProjectedLineResponse{
		Description: l.Description,
		Quantity:    Money(l.Quantity),
		UnitPrice:   Money(l.UnitPrice),
		Amount:      Money(l.Amount),
	}
}

// NewOccurrenceList maps occurrences; Items is never null.
func NewOccurrenceList(r DateRange, occs []model.PlanOccurrence) OccurrenceList {
	items := make([]OccurrenceResponse, len(occs))
	for i, o := range occs {
		items[i] = OccurrenceResponse{PlanID: o.PlanID, Index: o.Index, Date: o.Date.Format(dateLayout), JobID: o.JobID}
	}
	return OccurrenceList{Range: r, Items: items}
}

// NewProjectedInvoiceList maps projected invoices; Items is never null.
func NewProjectedInvoiceList(r DateRange, invs []model.ProjectedInvoice) ProjectedInvoiceList {
	items := make([]ProjectedInvoiceResponse, len(invs))
	for i, v := range invs {
		items[i] = ProjectedInvoiceResponse{
			PlanID:   v.PlanID,
			Ref:      v.Ref,
			Index:    v.Occurrence.Index,
			IssuedOn: v.IssuedOn.Format(dateLayout),
			DueOn:    v.DueOn.Format(dateLayout),
			Line:     newProjectedLine(v.Line),
			Subtotal: Money(v.Subtotal),
			Tax:      Money(v.Tax),
			Total:    Money(v.Total),
			Kind:     projectedKind,
		}
	}
	return ProjectedInvoiceList{Range: r, Items: items}
}

// NewProjectedEstimateResponse maps a projected estimate.
func NewProjectedEstimateResponse(r DateRange, e model.ProjectedEstimate) ProjectedEstimateResponse {
	return ProjectedEstimateResponse{
		PlanID:     e.PlanID,
		Range:      r,
		VisitCount: e.VisitCount,
		Line:       newProjectedLine(e.Line),
		Subtotal:   Money(e.Subtotal),
		Tax:        Money(e.Tax),
		Total:      Money(e.Total),
		Kind:       projectedKind,
	}
}
