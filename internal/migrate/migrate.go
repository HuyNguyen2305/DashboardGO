// Package migrate applies the embedded SQL migrations with golang-migrate.
package migrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"dashboardgo/migrations"
)

// ErrNoChange means the database was already at the requested version.
var ErrNoChange = migrate.ErrNoChange

// Runner applies migrations to one database.
type Runner struct {
	m *migrate.Migrate
}

// New opens a Runner for the database at a postgres:// or postgresql:// URL.
func New(databaseURL string) (*Runner, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, driverURL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("connect migrator: %w", err)
	}
	return &Runner{m: m}, nil
}

// Up applies every pending migration.
func (r *Runner) Up() error {
	if err := r.m.Up(); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down reverts the latest steps migrations. steps must be at least 1, so nothing is ever reverted by accident.
// Asking for more steps than are applied reverts them all and succeeds; golang-migrate reports that
// as ErrShortLimit only after it has already reverted everything.
func (r *Runner) Down(steps int) error {
	if steps < 1 {
		return fmt.Errorf("migrate down: steps must be at least 1, got %d", steps)
	}
	err := r.m.Steps(-steps)
	if errors.As(err, new(migrate.ErrShortLimit)) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate down %d: %w", steps, err)
	}
	return nil
}

// Version returns the current version and whether a failed migration left the database dirty.
// It returns 0, false when no migration has been applied.
func (r *Runner) Version() (uint, bool, error) {
	v, dirty, err := r.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("migrate version: %w", err)
	}
	return v, dirty, nil
}

// Force sets the version without running migrations, to recover from a dirty state after a manual fix.
func (r *Runner) Force(version int) error {
	if err := r.m.Force(version); err != nil {
		return fmt.Errorf("migrate force %d: %w", version, err)
	}
	return nil
}

// Close releases the database connection.
func (r *Runner) Close() error {
	srcErr, dbErr := r.m.Close()
	return errors.Join(srcErr, dbErr)
}

// driverURL switches the scheme to pgx5://, which selects golang-migrate's pgx v5 driver.
func driverURL(databaseURL string) string {
	for _, scheme := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(databaseURL, scheme) {
			return "pgx5://" + strings.TrimPrefix(databaseURL, scheme)
		}
	}
	return databaseURL
}
