//go:build integration

// Package testdb provides throwaway Postgres databases for integration tests.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/migrate"
)

// Start returns the DSN of a fresh, empty database that is removed when the test finishes.
//
// It needs TEST_POSTGRES_URL: a superuser URL to an existing Postgres server, on which
// it creates a uniquely named database.
func Start(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("TEST_POSTGRES_URL")
	if admin == "" {
		t.Fatal("set TEST_POSTGRES_URL to a superuser URL, " +
			"e.g. postgres://postgres:<pw>@localhost:5432/postgres?sslmode=disable")
	}
	return createDatabase(t, admin)
}

// createDatabase makes a throwaway database on the server at adminURL and returns its DSN.
func createDatabase(t *testing.T, adminURL string) string {
	t.Helper()
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	defer admin.Close(ctx)

	name := fmt.Sprintf("test_%d", time.Now().UnixNano())
	ident := pgx.Identifier{name}.Sanitize()
	_, err = admin.Exec(ctx, "CREATE DATABASE "+ident)
	require.NoError(t, err)
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Logf("drop test database %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})

	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// Migrator returns a migration runner for dsn, closed when the test finishes.
func Migrator(t *testing.T, dsn string) *migrate.Runner {
	t.Helper()
	m, err := migrate.New(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// StartMigrated starts a fresh database and applies all migrations.
func StartMigrated(t *testing.T) string {
	t.Helper()
	dsn := Start(t)
	require.NoError(t, Migrator(t, dsn).Up())
	return dsn
}
