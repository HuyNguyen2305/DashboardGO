//go:build integration

package recurring_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

func ids(plans []model.RecurringPlan) []int64 {
	out := make([]int64, len(plans))
	for i, p := range plans {
		out[i] = p.ID
	}
	return out
}

func TestCreateGetRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cust := e.customer(t, "Acme")
	loc := e.location(t, cust)
	svc := e.service(t, "Lawn Care", "40.00")
	staff := e.staff(t, "Sam")
	end := e.date(2027, time.March, 31)
	price := decimal.RequireFromString("55.50")

	created, err := e.repo.CreatePlan(ctx, model.NewRecurringPlan{
		CustomerID: cust, ServiceID: svc, LocationID: &loc, StaffID: &staff,
		Rule:     recurrence.Rule{Unit: recurrence.Month, Count: 2, Start: e.date(2026, time.October, 31), End: &end},
		Quantity: decimal.RequireFromString("2.50"), UnitPrice: &price,
		TaxRate: decimal.RequireFromString("0.0825"), PaymentTermsDays: 14,
	})
	require.NoError(t, err)

	got, err := e.repo.GetPlan(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, got)
	assert.Equal(t, cust, got.CustomerID)
	assert.Equal(t, svc, got.ServiceID)
	assert.Equal(t, "Lawn Care", got.ServiceName)
	assert.Equal(t, model.PlanStatusActive, got.Status)
	require.NotNil(t, got.LocationID)
	assert.Equal(t, loc, *got.LocationID)
	require.NotNil(t, got.StaffID)
	assert.Equal(t, staff, *got.StaffID)
	assert.Equal(t, recurrence.Month, got.Rule.Unit)
	assert.Equal(t, 2, got.Rule.Count)
	assert.True(t, e.date(2026, time.October, 31).Equal(got.Rule.Start))
	assert.Equal(t, e.tz, got.Rule.Start.Location())
	require.NotNil(t, got.Rule.End)
	assert.True(t, end.Equal(*got.Rule.End))
	assert.Equal(t, e.tz, got.Rule.End.Location())
	assert.Equal(t, "2.5", got.Quantity.String())
	assert.Equal(t, "55.5", got.UnitPrice.String())
	assert.Equal(t, "0.0825", got.TaxRate.String())
	assert.Equal(t, 14, got.PaymentTermsDays)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())

	open := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	assert.Nil(t, open.Rule.End)
	assert.Nil(t, open.LocationID)
	assert.Nil(t, open.StaffID)
}

func TestGetPlanNotFound(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.repo.GetPlan(ctx, 999)
	assert.ErrorIs(t, err, model.ErrPlanNotFound)
	_, err = e.repo.GetPlanForUpdate(ctx, 999)
	assert.ErrorIs(t, err, model.ErrPlanNotFound)
	_, err = e.repo.SetPlanStatus(ctx, 999, model.PlanStatusPaused)
	assert.ErrorIs(t, err, model.ErrPlanNotFound)
}

func TestCreatePlanCrossCustomerLocation(t *testing.T) {
	e := newEnv(t)
	a, b := e.customer(t, "A"), e.customer(t, "B")
	loc := e.location(t, b)
	svc := e.service(t, "Svc", "10.00")
	price := decimal.NewFromInt(10)

	_, err := e.repo.CreatePlan(context.Background(), model.NewRecurringPlan{
		CustomerID: a, ServiceID: svc, LocationID: &loc,
		Rule:     recurrence.Rule{Unit: recurrence.Week, Count: 1, Start: e.date(2026, time.October, 5)},
		Quantity: decimal.NewFromInt(1), UnitPrice: &price,
	})
	assert.ErrorIs(t, err, model.ErrInvalidReference)
}

func TestServicePriceAndCustomerExists(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := e.service(t, "Svc", "12.34")
	off := e.service(t, "Old", "5.00")
	_, err := e.pool.Exec(ctx, `UPDATE services SET is_active = false WHERE id = $1`, off)
	require.NoError(t, err)

	price, active, err := e.repo.ServicePrice(ctx, svc)
	require.NoError(t, err)
	assert.Equal(t, "12.34", price.String())
	assert.True(t, active)

	_, active, err = e.repo.ServicePrice(ctx, off)
	require.NoError(t, err)
	assert.False(t, active)

	_, _, err = e.repo.ServicePrice(ctx, 999)
	assert.ErrorIs(t, err, model.ErrInvalidReference)

	cust := e.customer(t, "C")
	ok, err := e.repo.CustomerExists(ctx, cust)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = e.repo.CustomerExists(ctx, 999)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestListPlansKeysetAndFilters(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c1, c2 := e.customer(t, "One"), e.customer(t, "Two")
	svc := e.service(t, "Svc", "10.00")
	for _, c := range []int64{c1, c2, c1, c2, c1} {
		e.seedPlan(t, planOpts{customerID: c, serviceID: svc})
	}
	_, err := e.repo.SetPlanStatus(ctx, 2, model.PlanStatusPaused)
	require.NoError(t, err)

	var pages [][]int64
	after := int64(0)
	for {
		got, err := e.repo.ListPlans(ctx, model.PlanListQuery{After: after, Limit: 2})
		require.NoError(t, err)
		if len(got) == 0 {
			break
		}
		pages = append(pages, ids(got))
		after = got[len(got)-1].ID
	}
	assert.Equal(t, [][]int64{{1, 2}, {3, 4}, {5}}, pages)

	got, err := e.repo.ListPlans(ctx, model.PlanListQuery{CustomerID: &c1, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 3, 5}, ids(got))
	assert.Equal(t, "Svc", got[0].ServiceName)

	paused := model.PlanStatusPaused
	got, err = e.repo.ListPlans(ctx, model.PlanListQuery{Status: &paused, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []int64{2}, ids(got))
}

func TestUpdatePlanAndSetStatus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cust := e.customer(t, "C")
	svc := e.service(t, "Svc", "10.00")
	plan := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})

	end := e.date(2026, time.December, 31)
	plan.Rule.End = &end
	plan.Rule.Count = 3
	plan.Quantity = decimal.NewFromInt(4)
	plan.UnitPrice = decimal.RequireFromString("19.99")
	plan.PaymentTermsDays = 30
	updated, err := e.repo.UpdatePlan(ctx, plan)
	require.NoError(t, err)
	assert.Equal(t, 3, updated.Rule.Count)
	require.NotNil(t, updated.Rule.End)
	assert.True(t, end.Equal(*updated.Rule.End))
	assert.Equal(t, "19.99", updated.UnitPrice.String())
	assert.Equal(t, 30, updated.PaymentTermsDays)
	assert.Equal(t, "Svc", updated.ServiceName)
	assert.True(t, updated.UpdatedAt.After(plan.UpdatedAt))

	paused, err := e.repo.SetPlanStatus(ctx, plan.ID, model.PlanStatusPaused)
	require.NoError(t, err)
	assert.Equal(t, model.PlanStatusPaused, paused.Status)

	missing := plan
	missing.ID = 999
	_, err = e.repo.UpdatePlan(ctx, missing)
	assert.ErrorIs(t, err, model.ErrPlanNotFound)

	other := e.customer(t, "Other")
	loc := e.location(t, other)
	updated.LocationID = &loc
	_, err = e.repo.UpdatePlan(ctx, updated)
	assert.ErrorIs(t, err, model.ErrInvalidReference)
}

func TestPlanHasLinkedJobsFrom(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cust := e.customer(t, "C")
	svc := e.service(t, "Svc", "10.00")
	p1 := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	p2 := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	e.job(t, cust, p1.ID, "2026-10-05", time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC))

	has, err := e.repo.PlanHasLinkedJobsFrom(ctx, p1.ID, e.date(2026, time.October, 5))
	require.NoError(t, err)
	assert.True(t, has, "a visit on the from date counts")
	has, err = e.repo.PlanHasLinkedJobsFrom(ctx, p1.ID, e.date(2026, time.October, 6))
	require.NoError(t, err)
	assert.False(t, has, "a visit before from does not")
	has, err = e.repo.PlanHasLinkedJobsFrom(ctx, p2.ID, e.date(2026, time.October, 5))
	require.NoError(t, err)
	assert.False(t, has)
}

func TestLinkedOccurrences(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cust := e.customer(t, "C")
	svc := e.service(t, "Svc", "10.00")
	p1 := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	p2 := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	at := time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC)
	j1 := e.job(t, cust, p1.ID, "2026-10-05", at)
	j2 := e.job(t, cust, p2.ID, "2026-10-12", at)
	e.job(t, cust, p1.ID, "2026-11-30", at)

	w := model.Window{From: e.date(2026, time.October, 1), To: e.date(2026, time.November, 1)}
	got, err := e.repo.LinkedOccurrences(ctx, []int64{p1.ID, p2.ID}, w)
	require.NoError(t, err)
	assert.Equal(t, model.LinkedVisits{
		p1.ID: {recurrence.DateKey(e.date(2026, time.October, 5)): j1},
		p2.ID: {recurrence.DateKey(e.date(2026, time.October, 12)): j2},
	}, got)

	empty, err := e.repo.LinkedOccurrences(ctx, nil, w)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)
}

func TestActivePlansOverlapping(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c1, c2 := e.customer(t, "One"), e.customer(t, "Two")
	svc := e.service(t, "Svc", "10.00")
	w := model.Window{From: e.date(2026, time.October, 1), To: e.date(2026, time.November, 1)}

	open := e.seedPlan(t, planOpts{customerID: c1, serviceID: svc, start: e.date(2026, time.January, 1)})
	other := e.seedPlan(t, planOpts{customerID: c2, serviceID: svc, start: e.date(2026, time.October, 31)})
	paused := e.seedPlan(t, planOpts{customerID: c1, serviceID: svc})
	_, err := e.repo.SetPlanStatus(ctx, paused.ID, model.PlanStatusPaused)
	require.NoError(t, err)
	endedBefore := e.date(2026, time.September, 30)
	e.seedPlan(t, planOpts{customerID: c1, serviceID: svc, start: e.date(2026, time.January, 1), end: &endedBefore})
	e.seedPlan(t, planOpts{customerID: c1, serviceID: svc, start: e.date(2026, time.November, 1)})
	endsOnFrom := e.date(2026, time.October, 1)
	edge := e.seedPlan(t, planOpts{customerID: c1, serviceID: svc, start: e.date(2026, time.January, 1), end: &endsOnFrom})

	got, err := e.repo.ActivePlansOverlapping(ctx, w)
	require.NoError(t, err)
	assert.Equal(t, []int64{open.ID, other.ID, edge.ID}, ids(got))
	assert.Equal(t, "Svc", got[0].ServiceName)

	got, err = e.repo.ActivePlansForCustomer(ctx, c1, w)
	require.NoError(t, err)
	assert.Equal(t, []int64{open.ID, edge.ID}, ids(got))
}

func TestGetPlanForUpdateBlocksConcurrentStatusChange(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	cust := e.customer(t, "C")
	svc := e.service(t, "Svc", "10.00")
	plan := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})

	locked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	// Never leave the first transaction parked on release, even when the test fails early.
	t.Cleanup(unlock)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- e.repo.WithTx(ctx, model.TxOptions{}, func(ctx context.Context) error {
			if _, err := e.repo.GetPlanForUpdate(ctx, plan.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-firstDone:
		t.Fatalf("first transaction failed before taking the lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first transaction did not take the lock")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- e.repo.WithTx(ctx, model.TxOptions{}, func(ctx context.Context) error {
			if _, err := e.repo.GetPlanForUpdate(ctx, plan.ID); err != nil {
				return err
			}
			_, err := e.repo.SetPlanStatus(ctx, plan.ID, model.PlanStatusPaused)
			return err
		})
	}()

	// Prove the second transaction is queued on the row lock, not just slow: some backend of
	// this database must be waiting on a lock.
	waitingOnLock := func() bool {
		var n int
		err := e.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n)
		require.NoError(t, err)
		return n > 0
	}
	deadline := time.Now().Add(5 * time.Second)
	for !waitingOnLock() {
		select {
		case err := <-secondDone:
			t.Fatalf("second transaction finished while the first held the lock: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second transaction never waited on the row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-secondDone:
		t.Fatalf("second transaction finished while the first held the lock: %v", err)
	default:
	}

	unlock()
	require.NoError(t, <-firstDone)
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("second transaction did not finish after the first committed")
	}

	got, err := e.repo.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PlanStatusPaused, got.Status)
}

func TestPlanIntFieldsOutOfRange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cust := e.customer(t, "C")
	svc := e.service(t, "Svc", "10.00")
	existing := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	tooBig := math.MaxInt32 + 1
	price := decimal.NewFromInt(10)

	newPlan := func(count, terms int) model.NewRecurringPlan {
		return model.NewRecurringPlan{
			CustomerID: cust, ServiceID: svc,
			Rule:     recurrence.Rule{Unit: recurrence.Week, Count: count, Start: e.date(2026, time.October, 5)},
			Quantity: decimal.NewFromInt(1), UnitPrice: &price, PaymentTermsDays: terms,
		}
	}
	for name, in := range map[string]model.NewRecurringPlan{
		"count": newPlan(tooBig, 0),
		"terms": newPlan(1, tooBig),
	} {
		t.Run("create "+name, func(t *testing.T) {
			_, err := e.repo.CreatePlan(ctx, in)
			assert.ErrorIs(t, err, model.ErrInvalidPlan)
		})
	}

	countPlan, termsPlan := existing, existing
	countPlan.Rule.Count = tooBig
	termsPlan.PaymentTermsDays = tooBig
	for name, p := range map[string]model.RecurringPlan{"count": countPlan, "terms": termsPlan} {
		t.Run("update "+name, func(t *testing.T) {
			_, err := e.repo.UpdatePlan(ctx, p)
			assert.ErrorIs(t, err, model.ErrInvalidPlan)
		})
	}

	plans, err := e.repo.ListPlans(ctx, model.PlanListQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, plans, 1, "nothing was inserted")
	assert.Equal(t, existing, plans[0], "nothing was updated")
}

func TestCreatePlanForeignKeyReasons(t *testing.T) {
	e := newEnv(t)
	a, b := e.customer(t, "A"), e.customer(t, "B")
	bobsLocation := e.location(t, b)
	svc := e.service(t, "Svc", "10.00")
	missing := int64(999999)
	price := decimal.NewFromInt(10)

	tests := []struct {
		name   string
		mutate func(*model.NewRecurringPlan)
		want   string
	}{
		{"missing customer", func(in *model.NewRecurringPlan) { in.CustomerID = missing }, "customer_id does not exist"},
		{"missing service", func(in *model.NewRecurringPlan) { in.ServiceID = missing }, "service_id does not exist"},
		{"missing staff", func(in *model.NewRecurringPlan) { in.StaffID = &missing }, "staff_id does not exist"},
		{"cross-customer location", func(in *model.NewRecurringPlan) { in.LocationID = &bobsLocation }, "location_id does not belong to the customer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := model.NewRecurringPlan{
				CustomerID: a, ServiceID: svc,
				Rule:     recurrence.Rule{Unit: recurrence.Week, Count: 1, Start: e.date(2026, time.October, 5)},
				Quantity: decimal.NewFromInt(1), UnitPrice: &price,
			}
			tc.mutate(&in)
			_, err := e.repo.CreatePlan(context.Background(), in)
			require.ErrorIs(t, err, model.ErrInvalidReference)
			assert.Contains(t, err.Error(), model.ErrInvalidReference.Error()+": "+tc.want)
			assert.NotContains(t, err.Error(), "fkey")
			assert.NotContains(t, err.Error(), "constraint")
		})
	}
}
