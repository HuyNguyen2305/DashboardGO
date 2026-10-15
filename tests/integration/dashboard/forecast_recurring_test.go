//go:build integration

package dashboard_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
)

func TestSalesForecastAddsRecurringWithoutDoubleCounting(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()

	var before dto.SalesForecastResponse
	env.getJSON(t, "/api/v1/dashboard/sales-forecast", &before)

	// A monthly plan at 100.00 + 10% tax from Nov 5; the Dec 5 visit already has a job of 100.00.
	// A second plan ended on Sep 30, before today, and must add nothing.
	var planID int64
	require.NoError(t, env.pool.QueryRow(ctx, `
		INSERT INTO recurring_plans (customer_id, service_id, interval_unit, interval_count, start_date, unit_price, tax_rate)
		VALUES (1, 1, 'month', 1, '2026-11-05', 100.00, 0.1) RETURNING id`).Scan(&planID))
	_, err := env.pool.Exec(ctx, `
		INSERT INTO recurring_plans (customer_id, service_id, interval_unit, interval_count, start_date, end_date, unit_price)
		VALUES (1, 1, 'month', 1, '2026-01-05', '2026-09-30', 500.00)`)
	require.NoError(t, err)
	_, err = env.pool.Exec(ctx, `
		INSERT INTO jobs (customer_id, service_id, status, total, scheduled_at, created_at, recurring_plan_id, occurrence_date)
		VALUES (1, 1, 'incomplete', 100.00, '2026-12-05 09:00+07', '2026-10-01 09:00+07', $1, '2026-12-05')`, planID)
	require.NoError(t, err)

	var got dto.SalesForecastResponse
	env.getJSON(t, "/api/v1/dashboard/sales-forecast", &got)
	require.Len(t, got.Months, 12)

	assert.Equal(t, "0.00", got.Months[9].Recurring, "October: nothing from the plans")
	assert.Equal(t, "100.00", got.Months[10].Recurring, "November: one unbooked visit, tax left out")
	assert.Equal(t, "0.00", got.Months[11].Recurring, "December: its only visit is booked as a job")
	assert.Equal(t, "100.00", got.RecurringTotal)

	assert.Equal(t, before.Months[10].Amount, got.Months[10].Amount, "November booked is unchanged")
	assert.Equal(t, addMoney(t, before.Months[11].Amount, "100.00"), got.Months[11].Amount, "December booked gains the job")
	assert.Equal(t, addMoney(t, before.Total, "100.00"), got.Total)
}
