package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
	"dashboardgo/internal/service"
	"dashboardgo/tests/mocks"
)

var errBoom = errors.New("boom")

func recurringLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	return loc
}

func newRecurringSvc(t *testing.T) (*service.RecurringService, *mocks.MockRecurringRepository) {
	t.Helper()
	loc := recurringLoc(t)
	fixed := time.Date(2026, time.October, 15, 9, 0, 0, 0, loc)
	repo := mocks.NewMockRecurringRepository(t)
	return service.NewRecurringService(repo, daterange.NewResolver(loc, func() time.Time { return fixed })), repo
}

func expectTx(repo *mocks.MockRecurringRepository) {
	repo.EXPECT().WithTx(mock.Anything, model.TxOptions{}, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ model.TxOptions, fn func(context.Context) error) error {
			return fn(ctx)
		})
}

func TestRecurringCreate(t *testing.T) {
	price := decimal.RequireFromString("80.00")
	custom := decimal.RequireFromString("95.50")
	weekly := recurrence.Rule{Unit: recurrence.Week, Count: 1}
	tests := []struct {
		name      string
		rule      recurrence.Rule
		unitPrice *decimal.Decimal
		active    bool
		priceErr  error
		wantErr   error
		wantCall  bool
		wantPrice decimal.Decimal
	}{
		{name: "nil price takes service price", rule: weekly, active: true, wantCall: true, wantPrice: price},
		{name: "explicit price kept", rule: weekly, unitPrice: &custom, active: true, wantCall: true, wantPrice: custom},
		{name: "inactive service", rule: weekly, active: false, wantErr: model.ErrInvalidReference},
		{name: "inactive service with explicit price", rule: weekly, unitPrice: &custom, active: false, wantErr: model.ErrInvalidReference},
		{name: "price lookup error wrapped", rule: weekly, priceErr: errBoom, wantErr: errBoom},
		{name: "invalid rule never reaches repo", rule: recurrence.Rule{Unit: recurrence.Week, Count: 0}, wantErr: recurrence.ErrInvalidRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			in := model.NewRecurringPlan{CustomerID: 1, ServiceID: 2, Rule: tc.rule, UnitPrice: tc.unitPrice}
			if tc.rule.Count > 0 {
				repo.EXPECT().ServicePrice(mock.Anything, int64(2)).Return(price, tc.active, tc.priceErr)
			}
			if tc.wantCall {
				want := in
				want.UnitPrice = &tc.wantPrice
				repo.EXPECT().CreatePlan(mock.Anything, want).Return(model.RecurringPlan{ID: 7}, nil)
			}
			got, err := svc.Create(context.Background(), in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int64(7), got.ID)
		})
	}
}

func TestRecurringCreateWrapsRepoError(t *testing.T) {
	svc, repo := newRecurringSvc(t)
	repo.EXPECT().ServicePrice(mock.Anything, int64(2)).Return(decimal.NewFromInt(1), true, nil)
	repo.EXPECT().CreatePlan(mock.Anything, mock.Anything).Return(model.RecurringPlan{}, model.ErrInvalidReference)
	_, err := svc.Create(context.Background(), model.NewRecurringPlan{ServiceID: 2, Rule: recurrence.Rule{Unit: recurrence.Day, Count: 1}})
	require.ErrorIs(t, err, model.ErrInvalidReference)
	assert.NotEqual(t, model.ErrInvalidReference.Error(), err.Error())
}

func TestRecurringGet(t *testing.T) {
	svc, repo := newRecurringSvc(t)
	repo.EXPECT().GetPlan(mock.Anything, int64(3)).Return(model.RecurringPlan{ID: 3}, nil).Once()
	got, err := svc.Get(context.Background(), 3)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got.ID)

	repo.EXPECT().GetPlan(mock.Anything, int64(4)).Return(model.RecurringPlan{}, model.ErrPlanNotFound).Once()
	_, err = svc.Get(context.Background(), 4)
	require.ErrorIs(t, err, model.ErrPlanNotFound)
	assert.NotEqual(t, model.ErrPlanNotFound.Error(), err.Error())
}

func plans(ids ...int64) []model.RecurringPlan {
	out := make([]model.RecurringPlan, len(ids))
	for i, id := range ids {
		out[i] = model.RecurringPlan{ID: id}
	}
	return out
}

func TestRecurringList(t *testing.T) {
	cursor := int64(2)
	tests := []struct {
		name       string
		limit      int
		wantLimit  int
		rows       []model.RecurringPlan
		wantItems  int
		wantCursor *int64
	}{
		{name: "zero limit defaults to 50", limit: 0, wantLimit: 50, rows: plans(1, 2), wantItems: 2},
		{name: "negative limit defaults to 50", limit: -3, wantLimit: 50, rows: plans(1), wantItems: 1},
		{name: "over max clamps to 200", limit: 500, wantLimit: 200, rows: plans(1), wantItems: 1},
		{name: "exactly limit rows has no cursor", limit: 2, wantLimit: 2, rows: plans(1, 2), wantItems: 2},
		{name: "extra row yields cursor", limit: 2, wantLimit: 2, rows: plans(1, 2, 3), wantItems: 2, wantCursor: &cursor},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			repo.EXPECT().ListPlans(mock.Anything, model.PlanListQuery{After: 9, Limit: tc.wantLimit + 1}).Return(tc.rows, nil)
			page, err := svc.List(context.Background(), model.PlanListQuery{After: 9, Limit: tc.limit})
			require.NoError(t, err)
			assert.Len(t, page.Items, tc.wantItems)
			assert.Equal(t, tc.wantCursor, page.NextCursor)
		})
	}
}

func TestRecurringListError(t *testing.T) {
	svc, repo := newRecurringSvc(t)
	repo.EXPECT().ListPlans(mock.Anything, mock.Anything).Return(nil, errBoom)
	_, err := svc.List(context.Background(), model.PlanListQuery{})
	require.ErrorIs(t, err, errBoom)
	assert.NotEqual(t, errBoom.Error(), err.Error())
}

func TestRecurringUpdate(t *testing.T) {
	loc := recurringLoc(t)
	start := time.Date(2026, time.October, 1, 0, 0, 0, 0, loc)
	base := model.RecurringPlan{
		ID: 5, Status: model.PlanStatusActive,
		Rule: recurrence.Rule{Unit: recurrence.Week, Count: 1, Start: start},
	}
	canceled := base
	canceled.Status = model.PlanStatusCanceled
	endBefore := start.AddDate(0, 0, -1)
	endAfter := start.AddDate(0, 1, 0)
	qty := model.Optional[decimal.Decimal]{Set: true, Value: decimal.NewFromInt(2)}

	tests := []struct {
		name       string
		plan       model.RecurringPlan
		patch      model.RecurringPlanPatch
		hasJobs    bool
		wantJobsQ  bool
		wantUpdate bool
		wantErr    error
	}{
		{name: "non-cadence change skips job check", plan: base, patch: model.RecurringPlanPatch{Quantity: qty}, wantUpdate: true},
		{
			name:       "valid end date",
			plan:       base,
			patch:      model.RecurringPlanPatch{EndDate: model.Optional[*time.Time]{Set: true, Value: &endAfter}},
			wantUpdate: true,
		},
		{
			name:       "cadence change without jobs updates",
			plan:       base,
			patch:      model.RecurringPlanPatch{IntervalCount: model.Optional[int]{Set: true, Value: 2}},
			wantJobsQ:  true,
			wantUpdate: true,
		},
		{
			name:      "cadence change with jobs is locked",
			plan:      base,
			patch:     model.RecurringPlanPatch{IntervalUnit: model.Optional[recurrence.Unit]{Set: true, Value: recurrence.Month}},
			hasJobs:   true,
			wantJobsQ: true,
			wantErr:   model.ErrCadenceLocked,
		},
		{
			name:    "end before start is invalid",
			plan:    base,
			patch:   model.RecurringPlanPatch{EndDate: model.Optional[*time.Time]{Set: true, Value: &endBefore}},
			wantErr: recurrence.ErrInvalidRule,
		},
		{name: "canceled plan rejects update", plan: canceled, patch: model.RecurringPlanPatch{Quantity: qty}, wantErr: model.ErrPlanCanceled},
		{
			// A client re-sending the whole form must not hit the cadence lock.
			name: "unchanged cadence with jobs updates",
			plan: base,
			patch: model.RecurringPlanPatch{
				IntervalUnit:  model.Optional[recurrence.Unit]{Set: true, Value: recurrence.Week},
				IntervalCount: model.Optional[int]{Set: true, Value: 1},
				StartDate:     model.Optional[time.Time]{Set: true, Value: time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)},
				Quantity:      qty,
			},
			hasJobs:    true,
			wantUpdate: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			expectTx(repo)
			repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(5)).Return(tc.plan, nil)
			if tc.wantJobsQ {
				repo.EXPECT().PlanHasLinkedJobs(mock.Anything, int64(5)).Return(tc.hasJobs, nil)
			}
			if tc.wantUpdate {
				repo.EXPECT().UpdatePlan(mock.Anything, tc.patch.Apply(tc.plan)).Return(model.RecurringPlan{ID: 5, Quantity: decimal.NewFromInt(9)}, nil)
			}
			got, err := svc.Update(context.Background(), 5, tc.patch)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.True(t, got.Quantity.Equal(decimal.NewFromInt(9)))
		})
	}
}

func TestRecurringUpdateRepoErrors(t *testing.T) {
	patch := model.RecurringPlanPatch{IntervalCount: model.Optional[int]{Set: true, Value: 2}}
	plan := model.RecurringPlan{ID: 5, Status: model.PlanStatusActive, Rule: recurrence.Rule{Unit: recurrence.Day, Count: 1}}

	t.Run("lock read", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		expectTx(repo)
		repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(5)).Return(model.RecurringPlan{}, model.ErrPlanNotFound)
		_, err := svc.Update(context.Background(), 5, patch)
		require.ErrorIs(t, err, model.ErrPlanNotFound)
		assert.NotEqual(t, model.ErrPlanNotFound.Error(), err.Error())
	})
	t.Run("linked jobs check", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		expectTx(repo)
		repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(5)).Return(plan, nil)
		repo.EXPECT().PlanHasLinkedJobs(mock.Anything, int64(5)).Return(false, errBoom)
		_, err := svc.Update(context.Background(), 5, patch)
		require.ErrorIs(t, err, errBoom)
		assert.NotEqual(t, errBoom.Error(), err.Error())
	})
	t.Run("update", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		expectTx(repo)
		repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(5)).Return(plan, nil)
		repo.EXPECT().PlanHasLinkedJobs(mock.Anything, int64(5)).Return(false, nil)
		repo.EXPECT().UpdatePlan(mock.Anything, mock.Anything).Return(model.RecurringPlan{}, errBoom)
		_, err := svc.Update(context.Background(), 5, patch)
		require.ErrorIs(t, err, errBoom)
		assert.NotEqual(t, errBoom.Error(), err.Error())
	})
}

func TestRecurringTransition(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		action     model.PlanAction
		wantStatus string
		wantSet    bool
		wantErr    error
	}{
		{name: "pause active", current: model.PlanStatusActive, action: model.PlanPause, wantStatus: model.PlanStatusPaused, wantSet: true},
		{name: "resume paused", current: model.PlanStatusPaused, action: model.PlanResume, wantStatus: model.PlanStatusActive, wantSet: true},
		{name: "cancel paused", current: model.PlanStatusPaused, action: model.PlanCancel, wantStatus: model.PlanStatusCanceled, wantSet: true},
		{name: "pause already paused is a no-op", current: model.PlanStatusPaused, action: model.PlanPause},
		{name: "cancel already canceled is a no-op", current: model.PlanStatusCanceled, action: model.PlanCancel},
		{name: "resume canceled is invalid", current: model.PlanStatusCanceled, action: model.PlanResume, wantErr: model.ErrInvalidTransition},
		{name: "unknown action is invalid", current: model.PlanStatusActive, action: "explode", wantErr: model.ErrInvalidTransition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			expectTx(repo)
			plan := model.RecurringPlan{ID: 8, Status: tc.current}
			repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(8)).Return(plan, nil)
			if tc.wantSet {
				repo.EXPECT().SetPlanStatus(mock.Anything, int64(8), tc.wantStatus).Return(model.RecurringPlan{ID: 8, Status: tc.wantStatus}, nil)
			}
			got, err := svc.Transition(context.Background(), 8, tc.action)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantSet {
				assert.Equal(t, tc.wantStatus, got.Status)
			} else {
				assert.Equal(t, plan, got)
			}
		})
	}
}

func TestRecurringTransitionRepoErrors(t *testing.T) {
	t.Run("lock read", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		expectTx(repo)
		repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(8)).Return(model.RecurringPlan{}, model.ErrPlanNotFound)
		_, err := svc.Transition(context.Background(), 8, model.PlanPause)
		require.ErrorIs(t, err, model.ErrPlanNotFound)
		assert.NotEqual(t, model.ErrPlanNotFound.Error(), err.Error())
	})
	t.Run("set status", func(t *testing.T) {
		svc, repo := newRecurringSvc(t)
		expectTx(repo)
		repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(8)).Return(model.RecurringPlan{Status: model.PlanStatusActive}, nil)
		repo.EXPECT().SetPlanStatus(mock.Anything, int64(8), model.PlanStatusPaused).Return(model.RecurringPlan{}, errBoom)
		_, err := svc.Transition(context.Background(), 8, model.PlanPause)
		require.ErrorIs(t, err, errBoom)
		assert.NotEqual(t, errBoom.Error(), err.Error())
	})
}

func TestRecurringUpdateEmptyPatchReturnsCurrentPlan(t *testing.T) {
	plan := model.RecurringPlan{ID: 5, Status: model.PlanStatusActive, Rule: recurrence.Rule{Unit: recurrence.Day, Count: 1}}
	canceled := plan
	canceled.Status = model.PlanStatusCanceled
	tests := []struct {
		name    string
		plan    model.RecurringPlan
		wantErr error
	}{
		{name: "active plan comes back unchanged", plan: plan},
		{name: "canceled plan still rejects", plan: canceled, wantErr: model.ErrPlanCanceled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newRecurringSvc(t)
			expectTx(repo)
			repo.EXPECT().GetPlanForUpdate(mock.Anything, int64(5)).Return(tc.plan, nil)
			// No UpdatePlan or PlanHasLinkedJobs expectation: the mock fails the test if either is called.

			got, err := svc.Update(context.Background(), 5, model.RecurringPlanPatch{})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.plan, got)
		})
	}
}
