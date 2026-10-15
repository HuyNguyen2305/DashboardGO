package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

func projDay(t *testing.T, y int, m time.Month, d int) time.Time {
	t.Helper()
	return time.Date(y, m, d, 0, 0, 0, 0, recurringLoc(t))
}

// projPlan is a plan whose rule starts at start and repeats every count units.
func projPlan(t *testing.T, id int64, status string, unit recurrence.Unit, count int, start time.Time, end *time.Time) model.RecurringPlan {
	t.Helper()
	return model.RecurringPlan{
		ID: id, CustomerID: 7, ServiceName: "Pest control", Status: status,
		Rule:     recurrence.Rule{Unit: unit, Count: count, Start: start, End: end},
		Quantity: decimal.NewFromInt(1), UnitPrice: decimal.RequireFromString("100.00"),
		TaxRate: decimal.RequireFromString("0.10"), PaymentTermsDays: 14,
	}
}

func occDates(occs []model.PlanOccurrence) []string {
	out := make([]string, 0, len(occs))
	for _, o := range occs {
		out = append(out, recurrence.DateKey(o.Date))
	}
	return out
}

func TestRecurringOccurrences(t *testing.T) {
	jobID := int64(55)
	ended := projDay(t, 2026, time.September, 30)
	type first struct {
		index int
		date  string
	}
	tests := []struct {
		name      string
		plan      model.RecurringPlan
		window    model.Window
		linked    model.LinkedVisits
		wantCall  bool
		wantFrom  time.Time
		wantDates []string
		wantFirst *first
		wantJob   map[string]int64
		wantErr   error
	}{
		{
			name:     "linked visit carries its job id",
			plan:     projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil),
			window:   model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.October, 30)},
			linked:   model.LinkedVisits{1: {"2026-10-22": jobID}},
			wantCall: true, wantFrom: projDay(t, 2026, time.October, 15),
			wantDates: []string{"2026-10-15", "2026-10-22", "2026-10-29"},
			wantJob:   map[string]int64{"2026-10-22": jobID},
		},
		{
			name:     "window before today is clamped to today",
			plan:     projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil),
			window:   model.Window{From: projDay(t, 2026, time.September, 1), To: projDay(t, 2026, time.October, 23)},
			wantCall: true, wantFrom: projDay(t, 2026, time.October, 15),
			wantDates: []string{"2026-10-15", "2026-10-22"},
		},
		{
			name:      "paused plan has no occurrences",
			plan:      projPlan(t, 1, model.PlanStatusPaused, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil),
			window:    model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.November, 15)},
			wantDates: []string{},
		},
		{
			name:    "25 month window is too large",
			plan:    projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil),
			window:  model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2028, time.November, 15)},
			wantErr: model.ErrWindowTooLarge,
		},
		{
			name:     "month-end anchor clamps and keeps index (review focus 1)",
			plan:     projPlan(t, 1, model.PlanStatusActive, recurrence.Month, 1, projDay(t, 2025, time.January, 31), nil),
			window:   model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.November, 30)},
			wantCall: true, wantFrom: projDay(t, 2026, time.October, 15),
			wantDates: []string{"2026-10-31"},
			wantFirst: &first{21, "2026-10-31"},
		},
		{
			name:     "plan ended before today has none (review focus 3)",
			plan:     projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.January, 1), &ended),
			window:   model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.November, 15)},
			wantCall: true, wantFrom: projDay(t, 2026, time.October, 15),
			wantDates: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			repo.EXPECT().GetPlan(mock.Anything, tt.plan.ID).Return(tt.plan, nil).Maybe()
			if tt.wantCall {
				repo.EXPECT().LinkedOccurrences(mock.Anything, []int64{tt.plan.ID}, model.Window{From: tt.wantFrom, To: tt.window.To}).
					Return(tt.linked, nil).Once()
			}
			got, err := svc.Occurrences(context.Background(), tt.plan.ID, tt.window)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantDates, occDates(got))
			if tt.wantFirst != nil {
				assert.Equal(t, tt.wantFirst.index, got[0].Index)
				assert.Equal(t, tt.wantFirst.date, recurrence.DateKey(got[0].Date))
			}
			for _, o := range got {
				if want, ok := tt.wantJob[recurrence.DateKey(o.Date)]; ok {
					require.NotNil(t, o.JobID)
					assert.Equal(t, want, *o.JobID)
				} else {
					assert.Nil(t, o.JobID)
				}
			}
		})
	}
}

func TestRecurringOccurrencesGetPlanError(t *testing.T) {
	svc, repo := newRecurringSvc(t)
	repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(model.RecurringPlan{}, errBoom)
	_, err := svc.Occurrences(context.Background(), 1, model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.November, 15)})
	require.ErrorIs(t, err, errBoom)
}

func TestRecurringProjectedInvoices(t *testing.T) {
	w := model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.October, 30)}
	tests := []struct {
		name      string
		status    string
		linked    model.LinkedVisits
		wantCall  bool
		wantRefs  []string
		wantTotal string
	}{
		{
			name: "linked visit removed", status: model.PlanStatusActive, linked: model.LinkedVisits{1: {"2026-10-22": 9}},
			wantCall: true, wantRefs: []string{"PRJ-1-2", "PRJ-1-4"}, wantTotal: "110.00",
		},
		{name: "canceled plan has none", status: model.PlanStatusCanceled, wantRefs: []string{}},
		{name: "paused plan has none", status: model.PlanStatusPaused, wantRefs: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			plan := projPlan(t, 1, tt.status, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil)
			repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(plan, nil)
			if tt.wantCall {
				repo.EXPECT().LinkedOccurrences(mock.Anything, []int64{1}, w).Return(tt.linked, nil).Once()
			}
			got, err := svc.ProjectedInvoices(context.Background(), 1, w)
			require.NoError(t, err)
			require.NotNil(t, got)
			refs := make([]string, 0, len(got))
			for _, inv := range got {
				refs = append(refs, inv.Ref)
			}
			assert.Equal(t, tt.wantRefs, refs)
			if tt.wantTotal != "" {
				assert.True(t, decimal.RequireFromString(tt.wantTotal).Equal(got[0].Total))
			}
		})
	}
}

func TestRecurringProjectedInvoicesTooLarge(t *testing.T) {
	svc, repo := newRecurringSvc(t)
	plan := projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil)
	repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(plan, nil).Maybe()
	_, err := svc.ProjectedInvoices(context.Background(), 1, model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2028, time.November, 15)})
	require.ErrorIs(t, err, model.ErrWindowTooLarge)
}

func TestRecurringProjectedEstimate(t *testing.T) {
	ended := projDay(t, 2026, time.September, 30)
	w := model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.October, 30)}
	tests := []struct {
		name       string
		status     string
		end        *time.Time
		wantErr    error
		wantVisits int
		wantTotal  string
	}{
		{name: "booked visits are kept (3 visits)", status: model.PlanStatusActive, wantVisits: 3, wantTotal: "330.00"},
		{name: "paused allowed", status: model.PlanStatusPaused, wantVisits: 3, wantTotal: "330.00"},
		{name: "canceled rejected", status: model.PlanStatusCanceled, wantErr: model.ErrPlanCanceled},
		{name: "plan ended before today (review focus 3)", status: model.PlanStatusActive, end: &ended, wantVisits: 0, wantTotal: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			plan := projPlan(t, 1, tt.status, recurrence.Week, 1, projDay(t, 2026, time.October, 1), tt.end)
			repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(plan, nil)
			got, err := svc.ProjectedEstimate(context.Background(), 1, w)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVisits, got.VisitCount)
			assert.True(t, decimal.RequireFromString(tt.wantTotal).Equal(got.Total), got.Total.String())
			if tt.wantVisits == 0 {
				assert.True(t, got.Subtotal.IsZero())
				assert.True(t, got.Tax.IsZero())
			}
		})
	}
}

func TestRecurringProjectedEstimateClampsAndCaps(t *testing.T) {
	t.Run("past window is empty", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		plan := projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.January, 1), nil)
		repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(plan, nil)
		got, err := svc.ProjectedEstimate(context.Background(), 1, model.Window{From: projDay(t, 2026, time.January, 1), To: projDay(t, 2026, time.February, 1)})
		require.NoError(t, err)
		assert.Zero(t, got.VisitCount)
		assert.True(t, got.Total.IsZero())
	})
	t.Run("25 month window is too large", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		plan := projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.January, 1), nil)
		repo.EXPECT().GetPlan(mock.Anything, int64(1)).Return(plan, nil).Maybe()
		_, err := svc.ProjectedEstimate(context.Background(), 1, model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2028, time.November, 15)})
		require.ErrorIs(t, err, model.ErrWindowTooLarge)
	})
}

func TestRecurringCustomerOccurrences(t *testing.T) {
	w := model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2026, time.October, 30)}
	t.Run("customer not found (review focus 5)", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		repo.EXPECT().CustomerExists(mock.Anything, int64(7)).Return(false, nil)
		_, err := svc.CustomerOccurrences(context.Background(), 7, w)
		require.ErrorIs(t, err, model.ErrCustomerNotFound)
	})
	t.Run("no plans gives empty list", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		repo.EXPECT().CustomerExists(mock.Anything, int64(7)).Return(true, nil)
		repo.EXPECT().ActivePlansForCustomer(mock.Anything, int64(7), w).Return(nil, nil)
		got, err := svc.CustomerOccurrences(context.Background(), 7, w)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Empty(t, got)
	})
	t.Run("13 month window is too large", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		repo.EXPECT().CustomerExists(mock.Anything, int64(7)).Return(true, nil).Maybe()
		_, err := svc.CustomerOccurrences(context.Background(), 7, model.Window{From: projDay(t, 2026, time.October, 15), To: projDay(t, 2027, time.November, 16)})
		require.ErrorIs(t, err, model.ErrWindowTooLarge)
	})
	t.Run("two plans merge by date then plan id with one linked query", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		p2 := projPlan(t, 2, model.PlanStatusActive, recurrence.Week, 1, projDay(t, 2026, time.October, 1), nil)
		p1 := projPlan(t, 1, model.PlanStatusActive, recurrence.Week, 2, projDay(t, 2026, time.October, 1), nil)
		repo.EXPECT().CustomerExists(mock.Anything, int64(7)).Return(true, nil)
		repo.EXPECT().ActivePlansForCustomer(mock.Anything, int64(7), w).Return([]model.RecurringPlan{p2, p1}, nil)
		repo.EXPECT().LinkedOccurrences(mock.Anything, []int64{2, 1}, w).Return(model.LinkedVisits{1: {"2026-10-15": 77}}, nil).Once()
		got, err := svc.CustomerOccurrences(context.Background(), 7, w)
		require.NoError(t, err)
		type row struct {
			plan int64
			date string
		}
		rows := []row{}
		for _, o := range got {
			rows = append(rows, row{o.PlanID, recurrence.DateKey(o.Date)})
		}
		assert.Equal(t, []row{
			{1, "2026-10-15"}, {2, "2026-10-15"}, {2, "2026-10-22"}, {1, "2026-10-29"}, {2, "2026-10-29"},
		}, rows)
		require.NotNil(t, got[0].JobID)
		assert.Equal(t, int64(77), *got[0].JobID)
		assert.Nil(t, got[1].JobID)
	})
}

func TestRecurringMonthlyRecurring(t *testing.T) {
	w := model.Window{From: projDay(t, 2026, time.January, 1), To: projDay(t, 2027, time.January, 1)}
	eff := model.Window{From: projDay(t, 2026, time.October, 15), To: w.To}
	t.Run("sums subtotals per month, skips linked and past months", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		monthly := func(id int64) model.RecurringPlan {
			return projPlan(t, id, model.PlanStatusActive, recurrence.Month, 1, projDay(t, 2026, time.January, 20), nil)
		}
		plans := []model.RecurringPlan{monthly(1), monthly(2), monthly(3)}
		repo.EXPECT().ActivePlansOverlapping(mock.Anything, eff).Return(plans, nil)
		repo.EXPECT().LinkedOccurrences(mock.Anything, []int64{1, 2, 3}, eff).
			Return(model.LinkedVisits{2: {"2026-11-20": 5}}, nil).Once()
		got, err := svc.MonthlyRecurring(context.Background(), w)
		require.NoError(t, err)
		require.Len(t, got, 3)
		wantMonths := []time.Time{projDay(t, 2026, time.October, 1), projDay(t, 2026, time.November, 1), projDay(t, 2026, time.December, 1)}
		wantAmounts := []string{"300.00", "200.00", "300.00"}
		for i := range got {
			assert.True(t, wantMonths[i].Equal(got[i].Month), got[i].Month.String())
			assert.True(t, decimal.RequireFromString(wantAmounts[i]).Equal(got[i].Amount), got[i].Amount.String())
		}
	})
	t.Run("no plans skips linked query", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		repo.EXPECT().ActivePlansOverlapping(mock.Anything, eff).Return(nil, nil)
		got, err := svc.MonthlyRecurring(context.Background(), w)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Empty(t, got)
	})
	t.Run("window entirely in the past is empty without queries", func(t *testing.T) {
		svc, _ := newRecurringSvc(t)
		got, err := svc.MonthlyRecurring(context.Background(), model.Window{From: projDay(t, 2025, time.January, 1), To: projDay(t, 2026, time.January, 1)})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Empty(t, got)
	})
	t.Run("repository error is wrapped", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		repo.EXPECT().ActivePlansOverlapping(mock.Anything, eff).Return(nil, errBoom)
		_, err := svc.MonthlyRecurring(context.Background(), w)
		require.ErrorIs(t, err, errBoom)
	})
}
