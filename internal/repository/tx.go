package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"dashboardgo/internal/model"
	"dashboardgo/internal/repository/db"
)

// txKey carries the open transaction in the context passed to withTx's fn.
type txKey struct{}

// withTx runs fn in one transaction on conn: every repository call made with the ctx given to
// fn uses it. It commits when fn returns nil and rolls back otherwise. A nested call joins the
// transaction already open.
func withTx(ctx context.Context, conn Conn, opts model.TxOptions, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	pgOpts := pgx.TxOptions{}
	if opts.ReadOnly {
		pgOpts.AccessMode = pgx.ReadOnly
	}
	if opts.Snapshot {
		pgOpts.IsoLevel = pgx.RepeatableRead
	}
	tx, err := conn.BeginTx(ctx, pgOpts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		// Roll back even if ctx was canceled, so the connection goes back to the pool clean.
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// queriesFor returns q bound to the transaction in ctx, if any.
func queriesFor(ctx context.Context, q *db.Queries) *db.Queries {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return q.WithTx(tx)
	}
	return q
}
