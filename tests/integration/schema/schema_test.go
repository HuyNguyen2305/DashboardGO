//go:build integration

package schema_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"dashboardgo/tests/integration/testdb"
)

const (
	pgCheckViolation      = "23514"
	pgForeignKeyViolation = "23503"
)

func TestMigrationsUpDownUp(t *testing.T) {
	m := testdb.Migrator(t, testdb.Start(t))

	require.NoError(t, m.Up())
	v, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.NoError(t, m.Down(int(v)), "every down migration must apply cleanly")
	v, _, err = m.Version()
	require.NoError(t, err)
	require.Zero(t, v)
	require.NoError(t, m.Up())
}

func TestMigrateDownMoreStepsThanAppliedRevertsAll(t *testing.T) {
	m := testdb.Migrator(t, testdb.StartMigrated(t))

	require.NoError(t, m.Down(99), "reverting everything is a success, not ErrShortLimit")
	v, dirty, err := m.Version()
	require.NoError(t, err)
	require.Zero(t, v)
	require.False(t, dirty)

	require.NoError(t, m.Up(), "the database must still migrate up cleanly")
}

// seed holds IDs that the constraint cases insert against.
type seed struct {
	customerID, locationID, invoiceID, estimateID, customerTagID, locationTagID int64
}

func seedData(t *testing.T, ctx context.Context, tx pgx.Tx) seed {
	t.Helper()
	var s seed
	q := func(sql string, dest *int64, args ...any) {
		require.NoError(t, tx.QueryRow(ctx, sql, args...).Scan(dest))
	}
	q(`INSERT INTO customers (name) VALUES ('Acme') RETURNING id`, &s.customerID)
	q(`INSERT INTO locations (customer_id, address) VALUES ($1, '1 Main St') RETURNING id`, &s.locationID, s.customerID)
	q(`INSERT INTO documents (doc_type, status, number, customer_id, due_date, total)
	   VALUES ('invoice', 'partial_paid', 'INV-1', $1, CURRENT_DATE, 100.00) RETURNING id`, &s.invoiceID, s.customerID)
	var paymentID int64
	q(`INSERT INTO payments (document_id, method, amount) VALUES ($1, 'check', 40.00) RETURNING id`, &paymentID, s.invoiceID)
	q(`INSERT INTO documents (doc_type, status, number, customer_id, expires_at, total)
	   VALUES ('estimate', 'pending', 'EST-1', $1, CURRENT_DATE, 50.00) RETURNING id`, &s.estimateID, s.customerID)
	q(`INSERT INTO tags (name, scope) VALUES ('vip', 'customer') RETURNING id`, &s.customerTagID)
	q(`INSERT INTO tags (name, scope) VALUES ('north', 'location') RETURNING id`, &s.locationTagID)
	return s
}

func TestSchemaConstraints(t *testing.T) {
	dsn := testdb.StartMigrated(t)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	cases := []struct {
		name     string
		sql      func(s seed) (string, []any)
		wantCode string // empty means the statement must succeed
	}{
		{
			name: "estimate with due_date is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id, due_date)
				        VALUES ('estimate', 'draft', 'EST-2', $1, CURRENT_DATE)`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "merged partial status is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id)
				        VALUES ('invoice', 'partial', 'INV-5', $1)`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "payment above the invoice total is rejected",
			sql: func(s seed) (string, []any) {
				// INV-1 is 100.00 with 40.00 paid.
				return `INSERT INTO payments (document_id, method, amount) VALUES ($1, 'cash', 60.01)`, []any{s.invoiceID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "payment up to the invoice total is accepted",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO payments (document_id, method, amount) VALUES ($1, 'cash', 60.00)`, []any{s.invoiceID}
			},
		},
		{
			name: "completed job without completed_at is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO jobs (customer_id, status, scheduled_at) VALUES ($1, 'completed', now())`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "completed job with completed_at is accepted",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO jobs (customer_id, status, scheduled_at, completed_at)
				        VALUES ($1, 'completed', now(), now())`, []any{s.customerID}
			},
		},
		{
			name: "invoice with expires_at is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id, expires_at)
				        VALUES ('invoice', 'draft', 'INV-2', $1, CURRENT_DATE)`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "invoice with estimate status is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id)
				        VALUES ('invoice', 'won', 'INV-3', $1)`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "estimate with invoice status is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id)
				        VALUES ('estimate', 'paid', 'EST-4', $1)`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "estimate converted to invoice is accepted",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO documents (doc_type, status, number, customer_id, source_document_id)
				        VALUES ('invoice', 'sent', 'INV-4', $1, $2)`, []any{s.customerID, s.estimateID}
			},
		},
		{
			name: "payment against invoice is accepted",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO payments (document_id, method, amount) VALUES ($1, 'stripe', 40.00)`, []any{s.invoiceID}
			},
		},
		{
			name: "payment against estimate is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO payments (document_id, method, amount) VALUES ($1, 'check', 10.00)`, []any{s.estimateID}
			},
			wantCode: pgForeignKeyViolation,
		},
		{
			name: "location tag on customer is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO customer_tags (customer_id, tag_id) VALUES ($1, $2)`, []any{s.customerID, s.locationTagID}
			},
			wantCode: pgForeignKeyViolation,
		},
		{
			name: "customer tag on location is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO location_tags (location_id, tag_id) VALUES ($1, $2)`, []any{s.locationID, s.customerTagID}
			},
			wantCode: pgForeignKeyViolation,
		},
		{
			name: "unknown customer status is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO customers (name, status) VALUES ('Bad', 'prospect')`, nil
			},
			wantCode: pgCheckViolation,
		},
		{
			name: "completed_at on incomplete job is rejected",
			sql: func(s seed) (string, []any) {
				return `INSERT INTO jobs (customer_id, status, scheduled_at, completed_at)
				        VALUES ($1, 'incomplete', now(), now())`, []any{s.customerID}
			},
			wantCode: pgCheckViolation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each case runs in its own rolled-back transaction so cases stay independent.
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()

			sql, args := tc.sql(seedData(t, ctx, tx))
			_, err = tx.Exec(ctx, sql, args...)

			if tc.wantCode == "" {
				require.NoError(t, err)
				return
			}
			var pgErr *pgconn.PgError
			require.True(t, errors.As(err, &pgErr), "expected a Postgres error, got %v", err)
			require.Equal(t, tc.wantCode, pgErr.Code, pgErr.Message)
		})
	}

	t.Run("generated columns compute balance and line amount", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		s := seedData(t, ctx, tx)

		var balance string
		require.NoError(t, tx.QueryRow(ctx, `SELECT balance::text FROM documents WHERE id = $1`, s.invoiceID).Scan(&balance))
		require.Equal(t, "60.00", balance)

		var amount string
		require.NoError(t, tx.QueryRow(ctx,
			`INSERT INTO document_items (document_id, description, quantity, unit_price)
			 VALUES ($1, 'Lawn care', 3, 19.99) RETURNING amount::text`, s.invoiceID).Scan(&amount))
		require.Equal(t, "59.97", amount)
	})

	t.Run("amount_paid is derived from payments", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		s := seedData(t, ctx, tx)

		paidAndBalance := func(id int64) string {
			t.Helper()
			var out string
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT amount_paid::text || '/' || balance::text FROM documents WHERE id = $1`, id).Scan(&out))
			return out
		}
		exec := func(sql string, args ...any) {
			t.Helper()
			_, err := tx.Exec(ctx, sql, args...)
			require.NoError(t, err)
		}
		var other, paymentID int64
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO documents (doc_type, status, number, customer_id, total)
		   VALUES ('invoice', 'sent', 'INV-9', $1, 50.00) RETURNING id`, s.customerID).Scan(&other))

		require.Equal(t, "40.00/60.00", paidAndBalance(s.invoiceID), "seeded 40.00 payment")

		require.NoError(t, tx.QueryRow(ctx,
			`INSERT INTO payments (document_id, method, amount) VALUES ($1, 'cash', 25.00) RETURNING id`, s.invoiceID).Scan(&paymentID))
		require.Equal(t, "65.00/35.00", paidAndBalance(s.invoiceID), "insert")

		exec(`UPDATE payments SET amount = 5.00 WHERE id = $1`, paymentID)
		require.Equal(t, "45.00/55.00", paidAndBalance(s.invoiceID), "amount change")

		exec(`UPDATE payments SET document_id = $1 WHERE id = $2`, other, paymentID)
		require.Equal(t, "40.00/60.00", paidAndBalance(s.invoiceID), "moved away")
		require.Equal(t, "5.00/45.00", paidAndBalance(other), "moved in")

		exec(`DELETE FROM payments WHERE id = $1`, paymentID)
		require.Equal(t, "0.00/50.00", paidAndBalance(other), "delete")

		exec(`UPDATE documents SET amount_paid = 99.00 WHERE id = $1`, s.invoiceID)
		require.Equal(t, "40.00/60.00", paidAndBalance(s.invoiceID), "a direct write is replaced by the payments sum")

		var estimatePaid string
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO documents (doc_type, status, number, customer_id, total, amount_paid)
		   VALUES ('estimate', 'draft', 'EST-9', $1, 10, 5) RETURNING amount_paid::text`, s.customerID).Scan(&estimatePaid))
		require.Equal(t, "0.00", estimatePaid, "estimates never have payments")
	})
}
