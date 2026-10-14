//go:build integration

package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/database"
	"dashboardgo/internal/model"
	"dashboardgo/internal/repository"
	"dashboardgo/tests/integration/testdb"
)

func TestWithTxReadsOneSnapshot(t *testing.T) {
	ctx := context.Background()
	pool, err := database.NewPool(ctx, database.PoolConfig{URL: testdb.StartMigrated(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var invoiceID int64
	require.NoError(t, pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO customers (name) VALUES ('Snapshot Co') RETURNING id)
		INSERT INTO documents (doc_type, status, number, customer_id, total)
		SELECT 'invoice', 'sent', 'INV-S', id, 1000 FROM c RETURNING id`).Scan(&invoiceID))
	received := time.Now().Add(-time.Hour)
	pay := func(amount string) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO payments (document_id, method, amount, received_at) VALUES ($1, 'cash', $2, $3)`,
			invoiceID, amount, received)
		require.NoError(t, err)
	}
	repo := repository.NewDashboardRepository(pool, 5*time.Second)
	q := model.StatsQuery{Start: received.Add(-time.Hour), End: received.Add(time.Hour)}
	collected := func(ctx context.Context) string {
		t.Helper()
		s, err := repo.Stats(ctx, q)
		require.NoError(t, err)
		return s.Collected.StringFixed(2)
	}
	snapshot := model.TxOptions{ReadOnly: true, Snapshot: true}

	pay("10.00")
	err = repo.WithTx(ctx, snapshot, func(ctx context.Context) error {
		assert.Equal(t, "10.00", collected(ctx))
		pay("5.00") // committed by another connection mid-transaction
		assert.Equal(t, "10.00", collected(ctx), "a snapshot doesn't see later commits")

		return repo.WithTx(ctx, snapshot, func(ctx context.Context) error {
			assert.Equal(t, "10.00", collected(ctx), "a nested call joins the open transaction")
			return nil
		})
	})
	require.NoError(t, err)
	assert.Equal(t, "15.00", collected(ctx), "outside the transaction the new payment shows")

	boom := errors.New("boom")
	err = repo.WithTx(ctx, snapshot, func(context.Context) error { return boom })
	assert.ErrorIs(t, err, boom, "fn's error is returned after rolling back")

	// The pool is still healthy after the rollback.
	assert.Equal(t, "15.00", collected(ctx))
}
