package model_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	require.NoError(t, err)
	return d
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestProjectInvoice(t *testing.T) {
	tests := []struct {
		name                                string
		qty, unit, rate                     string
		terms                               int
		visit                               time.Time
		wantAmount, wantTax, wantTotal, due string
	}{
		{"rounds amount half up", "1.5", "33.33", "0", 0, day(2026, 1, 5), "50.00", "0.00", "50.00", "2026-01-05"},
		{"tax 8.25", "1", "100", "0.0825", 0, day(2026, 1, 5), "100.00", "8.25", "108.25", "2026-01-05"},
		{"tiny tax rounds down", "1", "0.06", "0.0825", 0, day(2026, 1, 5), "0.06", "0.00", "0.06", "2026-01-05"},
		{"tax half away from zero", "1", "10.10", "0.05", 0, day(2026, 1, 5), "10.10", "0.51", "10.61", "2026-01-05"},
		{"zero rate", "2", "10", "0", 0, day(2026, 1, 5), "20.00", "0.00", "20.00", "2026-01-05"},
		{"due date crosses month", "1", "10", "0", 5, day(2026, 1, 30), "10.00", "0.00", "10.00", "2026-02-04"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := model.RecurringPlan{
				ID: 7, ServiceName: "Lawn care",
				Quantity: dec(t, tc.qty), UnitPrice: dec(t, tc.unit), TaxRate: dec(t, tc.rate),
				PaymentTermsDays: tc.terms,
			}
			occ := recurrence.Occurrence{Index: 3, Date: tc.visit}

			inv := model.ProjectInvoice(plan, occ)

			assert.Equal(t, "PRJ-7-3", inv.Ref)
			assert.Equal(t, int64(7), inv.PlanID)
			assert.Equal(t, occ, inv.Occurrence)
			assert.Equal(t, "Lawn care", inv.Line.Description)
			assert.Equal(t, tc.wantAmount, inv.Line.Amount.StringFixed(2))
			assert.Equal(t, tc.wantAmount, inv.Subtotal.StringFixed(2))
			assert.Equal(t, tc.wantTax, inv.Tax.StringFixed(2))
			assert.Equal(t, tc.wantTotal, inv.Total.StringFixed(2))
			assert.Equal(t, tc.visit, inv.IssuedOn)
			assert.Equal(t, tc.due, inv.DueOn.Format("2006-01-02"))
		})
	}
}

func TestProjectEstimateEqualsSumOfInvoices(t *testing.T) {
	plan := model.RecurringPlan{
		ID: 7, ServiceName: "Lawn care",
		Quantity: dec(t, "1"), UnitPrice: dec(t, "33.33"), TaxRate: dec(t, "0.0825"),
	}
	w := model.Window{From: day(2026, 1, 1), To: day(2027, 1, 1)}
	for _, n := range []int{0, 1, 3, 52} {
		t.Run(strconv.Itoa(n)+" visits", func(t *testing.T) {
			occs := make([]recurrence.Occurrence, n)
			sub, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
			for i := range occs {
				occs[i] = recurrence.Occurrence{Index: i, Date: day(2026, 1, 1).AddDate(0, 0, 7*i)}
				inv := model.ProjectInvoice(plan, occs[i])
				sub, tax, total = sub.Add(inv.Subtotal), tax.Add(inv.Tax), total.Add(inv.Total)
			}

			est := model.ProjectEstimate(plan, occs, w)

			assert.Equal(t, n, est.VisitCount)
			assert.True(t, sub.Equal(est.Subtotal), "subtotal %s vs %s", sub, est.Subtotal)
			assert.True(t, tax.Equal(est.Tax), "tax %s vs %s", tax, est.Tax)
			assert.True(t, total.Equal(est.Total), "total %s vs %s", total, est.Total)
			assert.True(t, est.Line.Quantity.Equal(decimal.NewFromInt(int64(n))))
			assert.True(t, est.Line.Amount.Equal(est.Subtotal))
			assert.Equal(t, int64(7), est.PlanID)
			assert.Equal(t, w.From, est.From)
			assert.Equal(t, w.To, est.To)
			assert.Equal(t, "Lawn care", est.Line.Description)
			if n == 0 {
				assert.True(t, est.Subtotal.IsZero() && est.Tax.IsZero() && est.Total.IsZero())
			}
		})
	}
}

func TestNextPlanStatus(t *testing.T) {
	tests := []struct {
		name    string
		current string
		action  model.PlanAction
		want    string
		wantErr error
	}{
		{"active pause", model.PlanStatusActive, model.PlanPause, model.PlanStatusPaused, nil},
		{"paused resume", model.PlanStatusPaused, model.PlanResume, model.PlanStatusActive, nil},
		{"active cancel", model.PlanStatusActive, model.PlanCancel, model.PlanStatusCanceled, nil},
		{"paused cancel", model.PlanStatusPaused, model.PlanCancel, model.PlanStatusCanceled, nil},
		{"paused pause idempotent", model.PlanStatusPaused, model.PlanPause, model.PlanStatusPaused, nil},
		{"active resume idempotent", model.PlanStatusActive, model.PlanResume, model.PlanStatusActive, nil},
		{"canceled resume", model.PlanStatusCanceled, model.PlanResume, "", model.ErrInvalidTransition},
		{"canceled pause", model.PlanStatusCanceled, model.PlanPause, "", model.ErrInvalidTransition},
		{"canceled cancel", model.PlanStatusCanceled, model.PlanCancel, model.PlanStatusCanceled, nil},
		{"unknown action", model.PlanStatusActive, model.PlanAction("explode"), "", model.ErrInvalidTransition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := model.NextPlanStatus(tc.current, tc.action)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func set[T any](v T) model.Optional[T] { return model.Optional[T]{Set: true, Value: v} }

func TestPatchChangesCadence(t *testing.T) {
	current := recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: day(2026, 1, 1)}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	tests := []struct {
		name  string
		patch model.RecurringPlanPatch
		want  bool
	}{
		{"empty", model.RecurringPlanPatch{}, false},
		{"location only", model.RecurringPlanPatch{LocationID: set[*int64](nil)}, false},
		{"quantity only", model.RecurringPlanPatch{Quantity: set(decimal.NewFromInt(2))}, false},
		{"end date only", model.RecurringPlanPatch{EndDate: set[*time.Time](nil)}, false},
		{"unit", model.RecurringPlanPatch{IntervalUnit: set(recurrence.Week)}, true},
		{"count", model.RecurringPlanPatch{IntervalCount: set(2)}, true},
		{"start", model.RecurringPlanPatch{StartDate: set(day(2026, 2, 1))}, true},
		{"same unit", model.RecurringPlanPatch{IntervalUnit: set(recurrence.Month)}, false},
		{"same count", model.RecurringPlanPatch{IntervalCount: set(1)}, false},
		{"same start", model.RecurringPlanPatch{StartDate: set(day(2026, 1, 1))}, false},
		{"same start date in another zone", model.RecurringPlanPatch{StartDate: set(time.Date(2026, 1, 1, 0, 0, 0, 0, hcm))}, false},
		{
			"full form with the same cadence",
			model.RecurringPlanPatch{IntervalUnit: set(recurrence.Month), IntervalCount: set(1), StartDate: set(day(2026, 1, 1))},
			false,
		},
		{
			"full form with one change",
			model.RecurringPlanPatch{IntervalUnit: set(recurrence.Month), IntervalCount: set(3), StartDate: set(day(2026, 1, 1))},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.patch.ChangesCadence(current))
		})
	}
}

func TestPatchIsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		patch model.RecurringPlanPatch
		want  bool
	}{
		{"zero patch", model.RecurringPlanPatch{}, true},
		{"location cleared", model.RecurringPlanPatch{LocationID: set[*int64](nil)}, false},
		{"staff", model.RecurringPlanPatch{StaffID: set[*int64](nil)}, false},
		{"end date", model.RecurringPlanPatch{EndDate: set[*time.Time](nil)}, false},
		{"unit", model.RecurringPlanPatch{IntervalUnit: set(recurrence.Week)}, false},
		{"count", model.RecurringPlanPatch{IntervalCount: set(2)}, false},
		{"start", model.RecurringPlanPatch{StartDate: set(day(2026, 2, 1))}, false},
		{"quantity", model.RecurringPlanPatch{Quantity: set(decimal.NewFromInt(2))}, false},
		{"unit price", model.RecurringPlanPatch{UnitPrice: set(decimal.Zero)}, false},
		{"tax rate", model.RecurringPlanPatch{TaxRate: set(decimal.Zero)}, false},
		{"payment terms", model.RecurringPlanPatch{PaymentTermsDays: set(0)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.patch.IsEmpty())
		})
	}
}

func TestPatchApply(t *testing.T) {
	loc, staff := int64(5), int64(6)
	end := day(2026, 12, 31)
	base := model.RecurringPlan{
		ID: 1, LocationID: &loc, StaffID: &staff,
		Rule:     recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: day(2026, 1, 1), End: &end},
		Quantity: decimal.NewFromInt(1), UnitPrice: decimal.NewFromInt(10), TaxRate: decimal.NewFromInt(0),
		PaymentTermsDays: 7,
	}

	t.Run("empty patch changes nothing", func(t *testing.T) {
		assert.Equal(t, base, model.RecurringPlanPatch{}.Apply(base))
	})

	t.Run("nil location clears it", func(t *testing.T) {
		got := model.RecurringPlanPatch{LocationID: set[*int64](nil)}.Apply(base)
		assert.Nil(t, got.LocationID)
		assert.Equal(t, &staff, got.StaffID)
	})

	t.Run("sets fields", func(t *testing.T) {
		newStaff := int64(9)
		newStart := day(2026, 3, 1)
		p := model.RecurringPlanPatch{
			StaffID:          set(&newStaff),
			EndDate:          set[*time.Time](nil),
			IntervalUnit:     set(recurrence.Day),
			IntervalCount:    set(3),
			StartDate:        set(newStart),
			Quantity:         set(decimal.NewFromInt(4)),
			UnitPrice:        set(decimal.NewFromInt(20)),
			TaxRate:          set(dec(t, "0.1")),
			PaymentTermsDays: set(30),
		}
		got := p.Apply(base)
		assert.Equal(t, &newStaff, got.StaffID)
		assert.Equal(t, &loc, got.LocationID)
		assert.Nil(t, got.Rule.End)
		assert.Equal(t, recurrence.Day, got.Rule.Unit)
		assert.Equal(t, 3, got.Rule.Count)
		assert.Equal(t, newStart, got.Rule.Start)
		assert.True(t, got.Quantity.Equal(decimal.NewFromInt(4)))
		assert.True(t, got.UnitPrice.Equal(decimal.NewFromInt(20)))
		assert.True(t, got.TaxRate.Equal(dec(t, "0.1")))
		assert.Equal(t, 30, got.PaymentTermsDays)
	})
}
