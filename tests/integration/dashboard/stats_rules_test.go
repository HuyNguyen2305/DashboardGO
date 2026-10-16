//go:build integration

package dashboard_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
)

// TestStatsRules adds rows on top of the screenshot seed (clock: 2026-10-12 10:00 GMT+7) that each probe one rule.
func TestStatsRules(t *testing.T) {
	env := newEnv(t)
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO jobs (customer_id, status, total, scheduled_at, completed_at, created_at, booked_online) VALUES
		  -- Scheduled in September, finished in October: counts as completed in October.
		  (1, 'completed',  10.00, '2026-09-30 09:00+07', '2026-10-01 08:00+07', '2026-09-20 09:00+07', false),
		  -- Online bookings that fell through don't count as booked online.
		  (1, 'canceled',  999.00, '2026-10-20 09:00+07', NULL,                  '2026-10-05 09:00+07', true),
		  (1, 'terminated', 50.00, '2026-10-21 09:00+07', NULL,                  '2026-10-06 09:00+07', true),
		  -- An online booking that is still on counts.
		  (1, 'reschedule', 30.00, '2026-10-22 09:00+07', NULL,                  '2026-10-07 09:00+07', true);
		-- A taxed invoice: revenue takes the subtotal, Total New Jobs the total.
		INSERT INTO documents (doc_type, status, number, customer_id, issued_at, subtotal, tax, total)
		VALUES ('invoice', 'sent', 'INV-TAX', 1, '2026-10-06 12:00+07', 100.00, 10.00, 110.00);`)
	require.NoError(t, err)

	var october dto.StatsResponse
	env.getJSON(t, "/api/v1/dashboard/stats", &october)
	assert.Equal(t, "1587.40", october.Revenue, "screenshot 1487.40 + subtotal 100.00")
	assert.Equal(t, int64(15), october.JobsCompleted, "screenshot 14 + the job finished on Oct 1")
	assert.Equal(t, "30.00", october.NewJobsBookedOnline, "only the rescheduled online booking")
	assert.Equal(t, "2319.62", october.TotalNewJobs, "screenshot 2209.62 + total 110.00")

	var september dto.StatsResponse
	env.getJSON(t, "/api/v1/dashboard/stats?range=last_month", &september)
	assert.Equal(t, int64(0), september.JobsCompleted, "scheduled in September is not completed in September")

	var year dto.YearSeriesResponse
	env.getJSON(t, "/api/v1/dashboard/revenue-per-month", &year)
	assert.Equal(t, "1587.40", year.Months[9].Amount, "October revenue uses the subtotal too")
}

func TestAgingCountsFailedInvoices(t *testing.T) {
	env := newEnv(t)
	// A failed invoice is still owed; due 45 days before the frozen today (Oct 12).
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO documents (doc_type, status, number, customer_id, issued_at, due_date, subtotal, total)
		VALUES ('invoice', 'failed', 'INV-F', 1, '2026-08-01 10:00+07', '2026-08-28', 40.00, 40.00)`)
	require.NoError(t, err)

	var got dto.AgingResponse
	env.getJSON(t, "/api/v1/dashboard/invoices/aging", &got)
	assert.Equal(t, "1012.91", got.TotalAmount, "screenshot 972.91 + the failed 40.00")
	assert.Equal(t, row("31_60", 2, "60.00", got.Rows[2].Percent), got.Rows[2], "INV-A7 20.00 + INV-F 40.00")
}

func TestPeriodWidgetsStopAtNow(t *testing.T) {
	env := newEnv(t)
	// The clock is frozen at 2026-10-12 10:00 GMT+7.
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO payments (document_id, method, amount, received_at)
		SELECT id, 'cash', v.amount, v.received_at
		  FROM documents, (VALUES (3.00, '2026-10-12 09:00+07'::timestamptz),   -- an hour ago: counts
		                          (5.00, '2026-10-12 11:00+07'::timestamptz))  -- in an hour: not yet
		                  v(amount, received_at)
		 WHERE number = 'INV-1'`)
	require.NoError(t, err)

	var stats dto.StatsResponse
	env.getJSON(t, "/api/v1/dashboard/stats", &stats)
	assert.Equal(t, "870.26", stats.Collected, "screenshot 867.26 + the 3.00 already received")
	assert.Equal(t, "2026-10-31", stats.Range.End, "the response still shows the whole month")

	var payments dto.BreakdownResponse
	env.getJSON(t, "/api/v1/dashboard/payments", &payments)
	assert.Equal(t, "870.26", payments.TotalAmount)
	assert.Contains(t, payments.Rows, row("cash", 1, "3.00", 0.34)) // 0.345%, gives up the spare 0.01

	// Documents dated after now are left out of every widget that counts what has happened.
	var invoices, estimates dto.BreakdownResponse
	var byStaff dto.StaffRevenueResponse
	var year dto.YearSeriesResponse
	env.getJSON(t, "/api/v1/dashboard/invoices", &invoices)
	env.getJSON(t, "/api/v1/dashboard/estimates", &estimates)
	env.getJSON(t, "/api/v1/dashboard/revenue-by-staff", &byStaff)
	env.getJSON(t, "/api/v1/dashboard/revenue-per-month", &year)

	_, err = env.pool.Exec(context.Background(), `
		INSERT INTO documents (doc_type, status, number, customer_id, issued_at, job_id, subtotal, total) VALUES
		  -- In an hour, on a staffed job: not revenue yet.
		  ('invoice', 'sent', 'INV-LATER', 1, '2026-10-12 11:00+07',
		   (SELECT id FROM jobs WHERE staff_id IS NOT NULL ORDER BY id LIMIT 1), 70.00, 70.00),
		  -- Next month: Revenue per Month must not show it yet.
		  ('invoice', 'sent', 'INV-NOV', 1, '2026-11-03 09:00+07', NULL, 80.00, 80.00);
		INSERT INTO documents (doc_type, status, number, customer_id, issued_at, total)
		VALUES ('estimate', 'pending', 'EST-LATER', 1, '2026-10-12 11:00+07', 90.00);`)
	require.NoError(t, err)

	var invoicesAfter, estimatesAfter dto.BreakdownResponse
	var byStaffAfter dto.StaffRevenueResponse
	var yearAfter dto.YearSeriesResponse
	env.getJSON(t, "/api/v1/dashboard/invoices", &invoicesAfter)
	env.getJSON(t, "/api/v1/dashboard/estimates", &estimatesAfter)
	env.getJSON(t, "/api/v1/dashboard/revenue-by-staff", &byStaffAfter)
	env.getJSON(t, "/api/v1/dashboard/revenue-per-month", &yearAfter)
	env.getJSON(t, "/api/v1/dashboard/stats", &stats)

	assert.Equal(t, invoices, invoicesAfter, "the invoice dated in an hour is left out")
	assert.Equal(t, estimates, estimatesAfter, "the estimate dated in an hour is left out")
	assert.Equal(t, byStaff, byStaffAfter, "the invoice dated in an hour is not staff revenue yet")
	assert.Equal(t, year, yearAfter, "Revenue per Month leaves out invoices dated after now")
	assert.Equal(t, stats.Revenue, yearAfter.Months[9].Amount, "October's bar matches the This Month revenue card")
	assert.Equal(t, "0.00", yearAfter.Months[10].Amount, "November has no revenue yet")
	assert.Len(t, yearAfter.Months, 12, "every month is still listed")
}

func TestSalesForecastCountsBookedJobs(t *testing.T) {
	env := newEnv(t)
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO jobs (customer_id, status, total, scheduled_at, created_at) VALUES
		  (1, 'canceled',   500.00, '2026-12-05 09:00+07', '2026-10-01 09:00+07'),  -- fell through: not booked
		  (1, 'reschedule',  40.00, '2026-12-06 09:00+07', '2026-10-01 09:00+07'),  -- still booked
		  (1, 'workpool',    25.00, '2026-12-31 23:30+07', '2026-10-01 09:00+07')   -- Dec 31 locally, Dec 31 16:30 UTC`)
	require.NoError(t, err)

	var got dto.YearSeriesResponse
	env.getJSON(t, "/api/v1/dashboard/sales-forecast", &got)
	assert.Equal(t, "65.00", got.Months[11].Amount, "December: reschedule 40.00 + workpool 25.00, canceled 500.00 left out")
}

func TestCustomerCounts(t *testing.T) {
	env := newEnv(t)
	// A second customer: two visits on a recurring service plus one non-recurring visit in October,
	// and two locations sharing the 1233 tag.
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO customers (name, created_at) VALUES ('Second Customer', '2026-01-20 10:00+07');
		INSERT INTO jobs (customer_id, service_id, status, total, scheduled_at, created_at)
		SELECT 2, s.id, 'incomplete', 10.00, v.at, '2026-10-01 09:00+07'
		  FROM (VALUES ('Every 21 Days', '2026-10-16 09:00+07'::timestamptz),
		               ('Every 21 Days', '2026-10-28 09:00+07'),
		               ('Blank Service', '2026-10-29 09:00+07')) v(service, at)
		  JOIN services s ON s.name = v.service;
		INSERT INTO locations (customer_id, address) VALUES (2, '3 North St'), (2, '4 South St');
		INSERT INTO location_tags (location_id, tag_id)
		SELECT l.id, t.id FROM locations l, tags t
		 WHERE l.customer_id = 2 AND t.scope = 'location' AND t.name = '1233';`)
	require.NoError(t, err)

	var services dto.ActiveServicesResponse
	env.getJSON(t, "/api/v1/dashboard/active-services", &services)
	assert.Equal(t, int64(21), services.TotalCount, "visits: seed 18 + 3")
	assert.Equal(t, int64(8), services.RecurringCount, "recurring visits: seed 6 + 2")
	assert.Equal(t, int64(2), services.CustomerCount)
	assert.Equal(t, int64(2), services.RecurringCustomerCount)
	for _, row := range services.Rows {
		if row.Name == "Every 21 Days" {
			assert.Equal(t, int64(4), row.Count, "visits")
			assert.Equal(t, int64(2), row.CustomerCount, "the second customer's two visits count once")
		}
	}

	var tags dto.TagsResponse
	env.getJSON(t, "/api/v1/dashboard/tags", &tags)
	require.NotEmpty(t, tags.Rows)
	assert.Equal(t, "1233", tags.Rows[0].Name)
	assert.Equal(t, int64(3), tags.Rows[0].Count, "three tagged locations")
	assert.Equal(t, ptr(int64(2)), tags.Rows[0].CustomerCount, "owned by two customers")

	var customerTags dto.TagsResponse
	env.getJSON(t, "/api/v1/dashboard/tags?scope=customer", &customerTags)
	for _, row := range customerTags.Rows {
		assert.Nil(t, row.CustomerCount, "customer tags already count customers")
	}
}

// serviceRow returns the Active Services row named name, failing the test when there is none.
func serviceRow(t *testing.T, w dto.ActiveServicesResponse, name string) dto.ServiceRow {
	t.Helper()
	for _, r := range w.Rows {
		if r.Name == name {
			return r
		}
	}
	require.Failf(t, "row not found", "no Active Services row %q", name)
	return dto.ServiceRow{}
}

// breakdownRow returns the breakdown row with key, failing the test when there is none.
func breakdownRow(t *testing.T, b dto.BreakdownResponse, key string) dto.BreakdownRow {
	t.Helper()
	for _, r := range b.Rows {
		if r.Key == key {
			return r
		}
	}
	require.Failf(t, "row not found", "no breakdown row %q", key)
	return dto.BreakdownRow{}
}

func TestActiveServicesCountsOpenAndPlanJobs(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	var before dto.ActiveServicesResponse
	env.getJSON(t, "/api/v1/dashboard/active-services", &before)

	// A monthly plan on Blank Service, which is not a recurring service; its Oct 26 visit has a job.
	var planID int64
	require.NoError(t, env.pool.QueryRow(ctx, `
		INSERT INTO recurring_plans (customer_id, service_id, interval_unit, interval_count, start_date, unit_price)
		SELECT 1, id, 'month', 1, '2026-10-26', 30.00 FROM services WHERE name = 'Blank Service'
		RETURNING id`).Scan(&planID))
	_, err := env.pool.Exec(ctx, `
		INSERT INTO jobs (customer_id, service_id, status, total, scheduled_at, created_at, recurring_plan_id, occurrence_date)
		SELECT 1, s.id, v.status, v.total, v.at, '2026-10-01 09:00+07', v.plan_id, v.occurrence_date
		  FROM services s,
		       (VALUES ('reschedule', 11.00, '2026-10-22 09:00+07'::timestamptz, NULL::bigint, NULL::date), -- open: counts
		               ('canceled',   12.00, '2026-10-23 09:00+07', NULL, NULL),                           -- not open
		               ('incomplete', 30.00, '2026-10-26 09:00+07', $1, '2026-10-26'))                    -- the plan visit
		       v(status, total, at, plan_id, occurrence_date)
		 WHERE s.name = 'Blank Service'`, planID)
	require.NoError(t, err)
	_, err = env.pool.Exec(ctx, `
		INSERT INTO jobs (customer_id, status, total, scheduled_at, created_at)
		VALUES (1, 'workpool', 13.00, '2026-10-24 09:00+07', '2026-10-01 09:00+07')`) // open, no service
	require.NoError(t, err)

	var got dto.ActiveServicesResponse
	env.getJSON(t, "/api/v1/dashboard/active-services", &got)
	assert.Equal(t, before.TotalCount+3, got.TotalCount, "reschedule + workpool + the plan visit; canceled left out")
	assert.Equal(t, addMoney(t, before.TotalAmount, "54.00"), got.TotalAmount, "11.00 + 13.00 + 30.00")
	assert.Equal(t, before.RecurringCount+1, got.RecurringCount, "the plan visit is recurring")
	assert.Equal(t, before.RecurringCustomerCount, got.RecurringCustomerCount, "the same single customer")

	blank := serviceRow(t, got, "Blank Service")
	assert.Equal(t, serviceRow(t, before, "Blank Service").Count+2, blank.Count, "reschedule + the plan visit")
	assert.Equal(t, int64(1), blank.RecurringCount, "only the plan visit")
	assert.False(t, blank.IsRecurring, "the service itself is still not recurring")
	assert.Equal(t, serviceRow(t, before, "No service").Count+1, serviceRow(t, got, "No service").Count, "the workpool job")
}

func TestFailedInvoicesAreNotRevenue(t *testing.T) {
	env := newEnv(t)
	var stats dto.StatsResponse
	var byStaff dto.StaffRevenueResponse
	var year dto.YearSeriesResponse
	var invoices dto.BreakdownResponse
	env.getJSON(t, "/api/v1/dashboard/stats", &stats)
	env.getJSON(t, "/api/v1/dashboard/revenue-by-staff", &byStaff)
	env.getJSON(t, "/api/v1/dashboard/revenue-per-month", &year)
	env.getJSON(t, "/api/v1/dashboard/invoices", &invoices)

	// Sent on Oct 5 for a staffed job, then the customer's payment failed.
	_, err := env.pool.Exec(context.Background(), `
		INSERT INTO documents (doc_type, status, number, customer_id, issued_at, job_id, subtotal, total)
		VALUES ('invoice', 'failed', 'INV-FAILED', 1, '2026-10-05 10:00+07',
		        (SELECT id FROM jobs WHERE staff_id IS NOT NULL ORDER BY id LIMIT 1), 40.00, 40.00)`)
	require.NoError(t, err)

	var statsAfter dto.StatsResponse
	var byStaffAfter dto.StaffRevenueResponse
	var yearAfter dto.YearSeriesResponse
	var invoicesAfter dto.BreakdownResponse
	env.getJSON(t, "/api/v1/dashboard/stats", &statsAfter)
	env.getJSON(t, "/api/v1/dashboard/revenue-by-staff", &byStaffAfter)
	env.getJSON(t, "/api/v1/dashboard/revenue-per-month", &yearAfter)
	env.getJSON(t, "/api/v1/dashboard/invoices", &invoicesAfter)

	assert.Equal(t, stats.Revenue, statsAfter.Revenue, "a failed invoice is not revenue")
	assert.Equal(t, stats.TotalNewJobs, statsAfter.TotalNewJobs, "nor a new job")
	assert.Equal(t, byStaff.TotalAmount, byStaffAfter.TotalAmount, "nor staff revenue")
	assert.Equal(t, year.Months[9].Amount, yearAfter.Months[9].Amount, "nor October revenue per month")

	failed, failedAfter := breakdownRow(t, invoices, "failed"), breakdownRow(t, invoicesAfter, "failed")
	assert.Equal(t, failed.Count+1, failedAfter.Count, "it is still listed among the invoices")
	assert.Equal(t, addMoney(t, failed.Amount, "40.00"), failedAfter.Amount)
}
