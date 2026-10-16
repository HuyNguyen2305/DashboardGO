package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
	"dashboardgo/internal/repository/db"
)

// pgForeignKeyViolation is the SQLSTATE of a foreign key violation.
const pgForeignKeyViolation = "23503"

// RecurringRepository reads and writes recurring plans.
type RecurringRepository struct {
	conn    Conn
	q       *db.Queries
	timeout time.Duration
	tz      *time.Location
}

// NewRecurringRepository uses conn (the shared pool), caps every query at timeout and returns
// plan dates as local midnight in tz.
func NewRecurringRepository(conn Conn, timeout time.Duration, tz *time.Location) *RecurringRepository {
	return &RecurringRepository{conn: conn, q: db.New(conn), timeout: timeout, tz: tz}
}

// WithTx runs fn in one transaction; see the package-level withTx.
func (r *RecurringRepository) WithTx(ctx context.Context, opts model.TxOptions, fn func(ctx context.Context) error) error {
	return withTx(ctx, r.conn, opts, fn)
}

func (r *RecurringRepository) queries(ctx context.Context) *db.Queries {
	return queriesFor(ctx, r.q)
}

// ServicePrice returns the catalog price of a service and whether it is active.
// A missing service is model.ErrInvalidReference.
func (r *RecurringRepository) ServicePrice(ctx context.Context, serviceID int64) (decimal.Decimal, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	row, err := r.queries(ctx).GetServicePrice(ctx, serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, false, fmt.Errorf("service %d: %w", serviceID, model.ErrInvalidReference)
	}
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("get service price: %w", err)
	}
	return row.Price, row.IsActive, nil
}

// CustomerExists reports whether a customer with this id exists.
func (r *RecurringRepository) CustomerExists(ctx context.Context, id int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	ok, err := r.queries(ctx).CustomerExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("check customer exists: %w", err)
	}
	return ok, nil
}

// CreatePlan inserts a plan. in.UnitPrice must be set (the service layer resolves the default).
func (r *RecurringRepository) CreatePlan(ctx context.Context, in model.NewRecurringPlan) (model.RecurringPlan, error) {
	if in.UnitPrice == nil {
		return model.RecurringPlan{}, errors.New("create plan: unit price is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	count, terms, err := planInts("create plan", in.Rule.Count, in.PaymentTermsDays)
	if err != nil {
		return model.RecurringPlan{}, err
	}
	q := r.queries(ctx)
	id, err := q.CreateRecurringPlan(ctx, db.CreateRecurringPlanParams{
		CustomerID:       in.CustomerID,
		LocationID:       in.LocationID,
		ServiceID:        in.ServiceID,
		StaffID:          in.StaffID,
		IntervalUnit:     string(in.Rule.Unit),
		IntervalCount:    count,
		StartDate:        toDBDate(in.Rule.Start),
		EndDate:          toDBDatePtr(in.Rule.End),
		Quantity:         in.Quantity,
		UnitPrice:        *in.UnitPrice,
		TaxRate:          in.TaxRate,
		PaymentTermsDays: terms,
	})
	if err != nil {
		return model.RecurringPlan{}, mapWriteError("create plan", err)
	}
	return r.reread(ctx, q, id)
}

// GetPlan returns one plan.
func (r *RecurringRepository) GetPlan(ctx context.Context, id int64) (model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	return r.reread(ctx, r.queries(ctx), id)
}

// GetPlanForUpdate returns one plan and row-locks it until the surrounding transaction ends.
func (r *RecurringRepository) GetPlanForUpdate(ctx context.Context, id int64) (model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	row, err := r.queries(ctx).GetRecurringPlanForUpdate(ctx, id)
	if err != nil {
		return model.RecurringPlan{}, mapReadError("get plan for update", err)
	}
	return r.toPlan(row.RecurringPlan, row.ServiceName), nil
}

// ListPlans returns one keyset page of plans ordered by id.
func (r *RecurringRepository) ListPlans(ctx context.Context, q model.PlanListQuery) ([]model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.queries(ctx).ListRecurringPlans(ctx, db.ListRecurringPlansParams{
		After:      q.After,
		CustomerID: q.CustomerID,
		Status:     q.Status,
		Lim:        clampInt32(q.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	out := make([]model.RecurringPlan, len(rows))
	for i, row := range rows {
		out[i] = r.toPlan(row.RecurringPlan, row.ServiceName)
	}
	return out, nil
}

// UpdatePlan writes the editable fields of p (not status or customer/service) and returns the stored plan.
func (r *RecurringRepository) UpdatePlan(ctx context.Context, p model.RecurringPlan) (model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	count, terms, err := planInts("update plan", p.Rule.Count, p.PaymentTermsDays)
	if err != nil {
		return model.RecurringPlan{}, err
	}
	q := r.queries(ctx)
	id, err := q.UpdateRecurringPlan(ctx, db.UpdateRecurringPlanParams{
		ID:               p.ID,
		LocationID:       p.LocationID,
		StaffID:          p.StaffID,
		IntervalUnit:     string(p.Rule.Unit),
		IntervalCount:    count,
		StartDate:        toDBDate(p.Rule.Start),
		EndDate:          toDBDatePtr(p.Rule.End),
		Quantity:         p.Quantity,
		UnitPrice:        p.UnitPrice,
		TaxRate:          p.TaxRate,
		PaymentTermsDays: terms,
	})
	if err != nil {
		return model.RecurringPlan{}, mapWriteError("update plan", err)
	}
	return r.reread(ctx, q, id)
}

// SetPlanStatus changes only the status of a plan.
func (r *RecurringRepository) SetPlanStatus(ctx context.Context, id int64, status string) (model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	q := r.queries(ctx)
	id, err := q.SetRecurringPlanStatus(ctx, db.SetRecurringPlanStatusParams{ID: id, Status: status})
	if err != nil {
		return model.RecurringPlan{}, mapWriteError("set plan status", err)
	}
	return r.reread(ctx, q, id)
}

// PlanHasLinkedJobsFrom reports whether a job fulfils a visit of the plan dated on or after from.
func (r *RecurringRepository) PlanHasLinkedJobsFrom(ctx context.Context, id int64, from time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	has, err := r.queries(ctx).RecurringPlanHasLinkedJobsFrom(ctx, db.RecurringPlanHasLinkedJobsFromParams{
		ID: id, FromDate: toDBDate(from),
	})
	if err != nil {
		return false, fmt.Errorf("check plan linked jobs: %w", err)
	}
	return has, nil
}

// ActivePlansForCustomer returns a customer's active plans that overlap the window.
func (r *RecurringRepository) ActivePlansForCustomer(ctx context.Context, customerID int64, w model.Window) ([]model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.queries(ctx).ActiveRecurringPlansForCustomer(ctx, db.ActiveRecurringPlansForCustomerParams{
		CustomerID: customerID, FromDate: toDBDate(w.From), ToDate: toDBDate(w.To),
	})
	if err != nil {
		return nil, fmt.Errorf("list active plans for customer: %w", err)
	}
	out := make([]model.RecurringPlan, len(rows))
	for i, row := range rows {
		out[i] = r.toPlan(row.RecurringPlan, row.ServiceName)
	}
	return out, nil
}

// ActivePlansOverlapping returns every active plan that overlaps the window.
func (r *RecurringRepository) ActivePlansOverlapping(ctx context.Context, w model.Window) ([]model.RecurringPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.queries(ctx).ActiveRecurringPlansOverlapping(ctx, db.ActiveRecurringPlansOverlappingParams{
		FromDate: toDBDate(w.From), ToDate: toDBDate(w.To),
	})
	if err != nil {
		return nil, fmt.Errorf("list active plans overlapping window: %w", err)
	}
	out := make([]model.RecurringPlan, len(rows))
	for i, row := range rows {
		out[i] = r.toPlan(row.RecurringPlan, row.ServiceName)
	}
	return out, nil
}

// LinkedOccurrences returns the jobs fulfilling visits of the given plans inside the window,
// keyed by plan id and recurrence.DateKey of the visit date. It never returns a nil map.
func (r *RecurringRepository) LinkedOccurrences(ctx context.Context, planIDs []int64, w model.Window) (model.LinkedVisits, error) {
	out := model.LinkedVisits{}
	if len(planIDs) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	rows, err := r.queries(ctx).LinkedRecurringVisits(ctx, db.LinkedRecurringVisitsParams{
		PlanIds: planIDs, FromDate: toDBDate(w.From), ToDate: toDBDate(w.To),
	})
	if err != nil {
		return nil, fmt.Errorf("list linked occurrences: %w", err)
	}
	for _, row := range rows {
		// Both columns are NULL together; the query's filters exclude NULLs.
		if row.RecurringPlanID == nil || row.OccurrenceDate == nil {
			continue
		}
		visits, ok := out[*row.RecurringPlanID]
		if !ok {
			visits = map[string]int64{}
			out[*row.RecurringPlanID] = visits
		}
		visits[recurrence.DateKey(r.fromDBDate(*row.OccurrenceDate))] = row.ID
	}
	return out, nil
}

// reread loads a plan by id with its service name.
func (r *RecurringRepository) reread(ctx context.Context, q *db.Queries, id int64) (model.RecurringPlan, error) {
	row, err := q.GetRecurringPlan(ctx, id)
	if err != nil {
		return model.RecurringPlan{}, mapReadError("get plan", err)
	}
	return r.toPlan(row.RecurringPlan, row.ServiceName), nil
}

func (r *RecurringRepository) toPlan(p db.RecurringPlan, serviceName string) model.RecurringPlan {
	var end *time.Time
	if p.EndDate != nil {
		e := r.fromDBDate(*p.EndDate)
		end = &e
	}
	return model.RecurringPlan{
		ID:          p.ID,
		CustomerID:  p.CustomerID,
		ServiceID:   p.ServiceID,
		LocationID:  p.LocationID,
		StaffID:     p.StaffID,
		ServiceName: serviceName,
		Status:      p.Status,
		Rule: recurrence.Rule{
			Unit:  recurrence.Unit(p.IntervalUnit),
			Count: int(p.IntervalCount),
			Start: r.fromDBDate(p.StartDate),
			End:   end,
		},
		Quantity:         p.Quantity,
		UnitPrice:        p.UnitPrice,
		TaxRate:          p.TaxRate,
		PaymentTermsDays: int(p.PaymentTermsDays),
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
	}
}

// fromDBDate turns a DATE (UTC midnight) into local midnight in the business timezone.
func (r *RecurringRepository) fromDBDate(d time.Time) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 0, 0, 0, 0, r.tz)
}

// toDBDate keeps the calendar date of t and drops the zone, as a DATE parameter.
func toDBDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func toDBDatePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := toDBDate(*t)
	return &d
}

func mapReadError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, model.ErrPlanNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// fkReasons says, per recurring_plans foreign key, which field is wrong, in words a client can read.
var fkReasons = map[string]string{
	"recurring_plans_customer_id_fkey":             "customer_id does not exist",
	"recurring_plans_service_id_fkey":              "service_id does not exist",
	"recurring_plans_staff_id_fkey":                "staff_id does not exist",
	"recurring_plans_location_id_customer_id_fkey": "location_id does not belong to the customer",
}

// mapWriteError maps a missing row to ErrPlanNotFound and a foreign key violation to
// ErrInvalidReference with the field at fault; the constraint name itself never reaches the message.
func mapWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		if reason, ok := fkReasons[pgErr.ConstraintName]; ok {
			return fmt.Errorf("%s: %w: %s", op, model.ErrInvalidReference, reason)
		}
		return fmt.Errorf("%s: %w", op, model.ErrInvalidReference)
	}
	return mapReadError(op, err)
}

// clampInt32 narrows a page size to int32, saturating instead of wrapping.
func clampInt32(n int) int32 {
	return int32(min(max(n, math.MinInt32), math.MaxInt32))
}

// planInts checks that interval_count and payment_terms_days fit their int32 columns, so an
// oversized value is rejected instead of being stored as something else.
func planInts(op string, count, termsDays int) (int32, int32, error) {
	for _, f := range []struct {
		name string
		n    int
	}{{"interval count", count}, {"payment terms days", termsDays}} {
		if f.n < math.MinInt32 || f.n > math.MaxInt32 {
			return 0, 0, fmt.Errorf("%s: %w: %s %d out of range", op, model.ErrInvalidPlan, f.name, f.n)
		}
	}
	return clampInt32(count), clampInt32(termsDays), nil
}
