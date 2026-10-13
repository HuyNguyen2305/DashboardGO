//go:build integration

package daterange_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/tests/integration/testdb"
)

// Payments just inside and just outside October 2026 (GMT+7) must be split exactly
// by the this_month range, whatever timezone the database session uses.
func TestThisMonthBoundariesInPostgres(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testdb.StartMigrated(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	at := func(m time.Month, d, hh, mm, ss, ns int) time.Time {
		return time.Date(2026, m, d, hh, mm, ss, ns, hcm)
	}

	var invoiceID int64
	require.NoError(t, pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (name) VALUES ('Boundary Co') RETURNING id)
		INSERT INTO documents (doc_type, status, number, customer_id, total)
		SELECT 'invoice', 'sent', 'INV-B', id, 1000 FROM c RETURNING id`).Scan(&invoiceID))

	payments := []struct {
		at     time.Time
		amount string
	}{
		{at(time.September, 30, 23, 59, 59, 999_999_000), "1.00"}, // out: last microsecond of September
		{at(time.October, 1, 0, 0, 0, 0), "10.00"},                // in: first instant of October
		{at(time.October, 31, 23, 59, 59, 999_999_000), "100.00"}, // in: last microsecond of October
		{at(time.November, 1, 0, 0, 0, 0), "1000.00"},             // out: first instant of November
	}
	for _, p := range payments {
		_, err := pool.Exec(ctx, `INSERT INTO payments (document_id, method, amount, received_at) VALUES ($1, 'check', $2, $3)`,
			invoiceID, p.amount, p.at)
		require.NoError(t, err)
	}

	now := at(time.October, 12, 10, 0, 0, 0)
	r, err := daterange.NewResolver(hcm, func() time.Time { return now }).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	for _, sessionTZ := range []string{"UTC", "Asia/Ho_Chi_Minh", "America/Los_Angeles"} {
		t.Run("session TimeZone "+sessionTZ, func(t *testing.T) {
			conn, err := pool.Acquire(ctx)
			require.NoError(t, err)
			defer conn.Release()

			_, err = conn.Exec(ctx, "SELECT set_config('TimeZone', $1, false)", sessionTZ)
			require.NoError(t, err)

			var count int
			var total string
			require.NoError(t, conn.QueryRow(ctx, `
				SELECT count(*), coalesce(sum(amount), 0)::text
				FROM payments
				WHERE received_at >= $1 AND received_at < $2`, r.Start, r.End).Scan(&count, &total))

			assert.Equal(t, 2, count)
			assert.Equal(t, "110.00", total)
		})
	}
}
