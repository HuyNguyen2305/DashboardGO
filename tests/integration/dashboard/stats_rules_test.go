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
