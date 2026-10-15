package service

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

// RecurringRepository stores recurring plans and reads what projections need.
type RecurringRepository interface {
	// WithTx runs fn in one transaction; repository calls made with fn's ctx use it.
	WithTx(ctx context.Context, opts model.TxOptions, fn func(ctx context.Context) error) error
	ServicePrice(ctx context.Context, serviceID int64) (decimal.Decimal, bool, error)
	CustomerExists(ctx context.Context, id int64) (bool, error)
	CreatePlan(ctx context.Context, in model.NewRecurringPlan) (model.RecurringPlan, error)
	GetPlan(ctx context.Context, id int64) (model.RecurringPlan, error)
	GetPlanForUpdate(ctx context.Context, id int64) (model.RecurringPlan, error)
	ListPlans(ctx context.Context, q model.PlanListQuery) ([]model.RecurringPlan, error)
	UpdatePlan(ctx context.Context, p model.RecurringPlan) (model.RecurringPlan, error)
	SetPlanStatus(ctx context.Context, id int64, status string) (model.RecurringPlan, error)
	PlanHasLinkedJobs(ctx context.Context, id int64) (bool, error)
	ActivePlansForCustomer(ctx context.Context, customerID int64, w model.Window) ([]model.RecurringPlan, error)
	ActivePlansOverlapping(ctx context.Context, w model.Window) ([]model.RecurringPlan, error)
	LinkedOccurrences(ctx context.Context, planIDs []int64, w model.Window) (model.LinkedVisits, error)
}

// RecurringService manages recurring service plans.
type RecurringService struct {
	repo  RecurringRepository
	dates *daterange.Resolver
}

// NewRecurringService builds a RecurringService; dates is the clock.
func NewRecurringService(repo RecurringRepository, dates *daterange.Resolver) *RecurringService {
	return &RecurringService{repo: repo, dates: dates}
}

// Page size bounds for List.
const (
	defaultPlanPageSize = 50
	maxPlanPageSize     = 200
)

// Create validates the rule and service, fills in the catalog price when none is given, and stores the plan.
func (s *RecurringService) Create(ctx context.Context, in model.NewRecurringPlan) (model.RecurringPlan, error) {
	if err := in.Rule.Validate(); err != nil {
		return model.RecurringPlan{}, fmt.Errorf("validate rule: %w", err)
	}
	price, active, err := s.repo.ServicePrice(ctx, in.ServiceID)
	if err != nil {
		return model.RecurringPlan{}, fmt.Errorf("plan service %d: %w", in.ServiceID, err)
	}
	if !active {
		return model.RecurringPlan{}, fmt.Errorf("service %d is inactive: %w", in.ServiceID, model.ErrInvalidReference)
	}
	if in.UnitPrice == nil {
		in.UnitPrice = &price
	}
	plan, err := s.repo.CreatePlan(ctx, in)
	if err != nil {
		return model.RecurringPlan{}, fmt.Errorf("plan for customer %d: %w", in.CustomerID, err)
	}
	return plan, nil
}

// Get returns one plan.
func (s *RecurringService) Get(ctx context.Context, id int64) (model.RecurringPlan, error) {
	plan, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return model.RecurringPlan{}, fmt.Errorf("plan %d: %w", id, err)
	}
	return plan, nil
}

// List returns one keyset page of plans; the limit defaults to 50 and is capped at 200.
func (s *RecurringService) List(ctx context.Context, q model.PlanListQuery) (model.PlanPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPlanPageSize
	}
	limit = min(limit, maxPlanPageSize)
	q.Limit = limit + 1 // one extra row tells whether another page follows
	items, err := s.repo.ListPlans(ctx, q)
	if err != nil {
		return model.PlanPage{}, fmt.Errorf("plan page after %d: %w", q.After, err)
	}
	page := model.PlanPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = &items[limit-1].ID
	}
	return page, nil
}

// Update applies patch under a row lock. Cadence fields only change while the plan has
// no linked jobs, since those jobs are keyed by occurrence date.
func (s *RecurringService) Update(ctx context.Context, id int64, patch model.RecurringPlanPatch) (model.RecurringPlan, error) {
	var out model.RecurringPlan
	err := s.repo.WithTx(ctx, model.TxOptions{}, func(ctx context.Context) error {
		plan, err := s.repo.GetPlanForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("update plan %d: %w", id, err)
		}
		if plan.Status == model.PlanStatusCanceled {
			return fmt.Errorf("update plan %d: %w", id, model.ErrPlanCanceled)
		}
		if patch.IsEmpty() {
			out = plan
			return nil
		}
		patched := patch.Apply(plan)
		if err := patched.Rule.Validate(); err != nil {
			return fmt.Errorf("validate patched rule: %w", err)
		}
		// Only a real change of unit, count or start counts, not the same values re-sent.
		if patch.ChangesCadence(plan.Rule) {
			locked, err := s.repo.PlanHasLinkedJobs(ctx, id)
			if err != nil {
				return fmt.Errorf("plan %d: %w", id, err)
			}
			if locked {
				return fmt.Errorf("plan %d: %w", id, model.ErrCadenceLocked)
			}
		}
		out, err = s.repo.UpdatePlan(ctx, patched)
		if err != nil {
			return fmt.Errorf("plan %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return model.RecurringPlan{}, err
	}
	return out, nil
}

// Transition moves a plan to the status action leads to. Repeating an action is a no-op.
func (s *RecurringService) Transition(ctx context.Context, id int64, action model.PlanAction) (model.RecurringPlan, error) {
	var out model.RecurringPlan
	err := s.repo.WithTx(ctx, model.TxOptions{}, func(ctx context.Context) error {
		plan, err := s.repo.GetPlanForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("transition plan %d: %w", id, err)
		}
		next, err := model.NextPlanStatus(plan.Status, action)
		if err != nil {
			return fmt.Errorf("transition plan %d: %w", id, err)
		}
		if next == plan.Status {
			out = plan
			return nil
		}
		out, err = s.repo.SetPlanStatus(ctx, id, next)
		if err != nil {
			return fmt.Errorf("plan %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return model.RecurringPlan{}, err
	}
	return out, nil
}

// Longest requested projection window, in months.
const (
	MaxPlanWindowMonths     = 24 // plan-level requests
	MaxCustomerWindowMonths = 12 // customer-level requests
)

// checkWindow rejects a requested window longer than maxMonths.
func checkWindow(w model.Window, maxMonths int) error {
	if w.To.After(w.From.AddDate(0, maxMonths, 0)) {
		return fmt.Errorf("window of more than %d months: %w", maxMonths, model.ErrWindowTooLarge)
	}
	return nil
}

// effectiveWindow starts w at today, since past visits are not projected.
// ok is false when nothing is left to project.
func (s *RecurringService) effectiveWindow(w model.Window) (eff model.Window, ok bool) {
	eff = model.Window{From: w.From, To: w.To}
	if today := s.dates.Today(); eff.From.Before(today) {
		eff.From = today
	}
	return eff, eff.From.Before(eff.To)
}

// planOccurrences returns the cadence visits of plan in w, each with its linked job.
func planOccurrences(plan model.RecurringPlan, w model.Window, linked model.LinkedVisits) []model.PlanOccurrence {
	occs := plan.Rule.Between(w.From, w.To)
	out := make([]model.PlanOccurrence, 0, len(occs))
	for _, o := range occs {
		po := model.PlanOccurrence{PlanID: plan.ID, Occurrence: o}
		if id, ok := linked[plan.ID][recurrence.DateKey(o.Date)]; ok {
			po.JobID = &id
		}
		out = append(out, po)
	}
	return out
}

// Occurrences lists the visits of an active plan from today on, each with its linked job if any.
func (s *RecurringService) Occurrences(ctx context.Context, id int64, w model.Window) ([]model.PlanOccurrence, error) {
	if err := checkWindow(w, MaxPlanWindowMonths); err != nil {
		return nil, fmt.Errorf("occurrences: %w", err)
	}
	plan, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("occurrences: %w", err)
	}
	eff, ok := s.effectiveWindow(w)
	if plan.Status != model.PlanStatusActive || !ok {
		return []model.PlanOccurrence{}, nil
	}
	linked, err := s.repo.LinkedOccurrences(ctx, []int64{id}, eff)
	if err != nil {
		return nil, fmt.Errorf("occurrences: %w", err)
	}
	return planOccurrences(plan, eff, linked), nil
}

// ProjectedInvoices lists the invoices of an active plan's visits that have no job yet.
func (s *RecurringService) ProjectedInvoices(ctx context.Context, id int64, w model.Window) ([]model.ProjectedInvoice, error) {
	if err := checkWindow(w, MaxPlanWindowMonths); err != nil {
		return nil, fmt.Errorf("projected invoices: %w", err)
	}
	plan, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("projected invoices: %w", err)
	}
	eff, ok := s.effectiveWindow(w)
	if plan.Status != model.PlanStatusActive || !ok {
		return []model.ProjectedInvoice{}, nil
	}
	linked, err := s.repo.LinkedOccurrences(ctx, []int64{id}, eff)
	if err != nil {
		return nil, fmt.Errorf("projected invoices: %w", err)
	}
	out := []model.ProjectedInvoice{}
	for _, po := range planOccurrences(plan, eff, linked) {
		if po.JobID == nil {
			out = append(out, model.ProjectInvoice(plan, po.Occurrence))
		}
	}
	return out, nil
}

// ProjectedEstimate prices every visit of an active or paused plan from today on, booked or not.
func (s *RecurringService) ProjectedEstimate(ctx context.Context, id int64, w model.Window) (model.ProjectedEstimate, error) {
	if err := checkWindow(w, MaxPlanWindowMonths); err != nil {
		return model.ProjectedEstimate{}, fmt.Errorf("projected estimate: %w", err)
	}
	plan, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return model.ProjectedEstimate{}, fmt.Errorf("projected estimate: %w", err)
	}
	if plan.Status == model.PlanStatusCanceled {
		return model.ProjectedEstimate{}, fmt.Errorf("projected estimate for plan %d: %w", id, model.ErrPlanCanceled)
	}
	eff, _ := s.effectiveWindow(w)
	return model.ProjectEstimate(plan, plan.Rule.Between(eff.From, eff.To), eff), nil
}

// CustomerOccurrences lists the visits of all of a customer's active plans from today on,
// ordered by date then plan ID.
func (s *RecurringService) CustomerOccurrences(ctx context.Context, customerID int64, w model.Window) ([]model.PlanOccurrence, error) {
	exists, err := s.repo.CustomerExists(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("customer occurrences: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("customer occurrences for customer %d: %w", customerID, model.ErrCustomerNotFound)
	}
	if err := checkWindow(w, MaxCustomerWindowMonths); err != nil {
		return nil, fmt.Errorf("customer occurrences: %w", err)
	}
	eff, ok := s.effectiveWindow(w)
	if !ok {
		return []model.PlanOccurrence{}, nil
	}
	plans, err := s.repo.ActivePlansForCustomer(ctx, customerID, eff)
	if err != nil {
		return nil, fmt.Errorf("customer occurrences: %w", err)
	}
	if len(plans) == 0 {
		return []model.PlanOccurrence{}, nil
	}
	linked, err := s.repo.LinkedOccurrences(ctx, planIDs(plans), eff)
	if err != nil {
		return nil, fmt.Errorf("customer occurrences: %w", err)
	}
	out := []model.PlanOccurrence{}
	for _, p := range plans {
		out = append(out, planOccurrences(p, eff, linked)...)
	}
	slices.SortFunc(out, func(a, b model.PlanOccurrence) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.PlanID, b.PlanID)
	})
	return out, nil
}

// MonthlyRecurring sums the pre-tax amount of unbooked visits of active plans per local
// month from today on; only months with visits appear. It feeds the Sales Forecast.
func (s *RecurringService) MonthlyRecurring(ctx context.Context, w model.Window) ([]model.MonthlyAmount, error) {
	eff, ok := s.effectiveWindow(w)
	if !ok {
		return []model.MonthlyAmount{}, nil
	}
	plans, err := s.repo.ActivePlansOverlapping(ctx, eff)
	if err != nil {
		return nil, fmt.Errorf("monthly recurring: %w", err)
	}
	if len(plans) == 0 {
		return []model.MonthlyAmount{}, nil
	}
	linked, err := s.repo.LinkedOccurrences(ctx, planIDs(plans), eff)
	if err != nil {
		return nil, fmt.Errorf("monthly recurring: %w", err)
	}
	// Keyed by calendar month: time.Time values from different plans may carry
	// distinct *Location pointers and would not compare equal as map keys.
	byMonth := map[string]*model.MonthlyAmount{}
	for _, p := range plans {
		for _, po := range planOccurrences(p, eff, linked) {
			if po.JobID != nil {
				continue
			}
			y, m, _ := po.Date.Date()
			key := fmt.Sprintf("%04d-%02d", y, m)
			if byMonth[key] == nil {
				byMonth[key] = &model.MonthlyAmount{Month: time.Date(y, m, 1, 0, 0, 0, 0, po.Date.Location())}
			}
			byMonth[key].Amount = byMonth[key].Amount.Add(model.ProjectInvoice(p, po.Occurrence).Subtotal)
		}
	}
	out := make([]model.MonthlyAmount, 0, len(byMonth))
	for _, ma := range byMonth {
		out = append(out, *ma)
	}
	slices.SortFunc(out, func(a, b model.MonthlyAmount) int { return a.Month.Compare(b.Month) })
	return out, nil
}

func planIDs(plans []model.RecurringPlan) []int64 {
	ids := make([]int64, len(plans))
	for i, p := range plans {
		ids[i] = p.ID
	}
	return ids
}
