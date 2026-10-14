package devdata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Presets accepted by Seed.
const (
	PresetScreenshot = "screenshot"
	PresetDemo       = "demo"
)

// Errors returned by Seed.
var (
	ErrUnknownPreset = errors.New("unknown seed preset")
	ErrNotEmpty      = errors.New("database already has data")
)

// SeedOptions selects what Seed loads.
type SeedOptions struct {
	Preset string
	Reset  bool        // empty every table and restart IDs first
	Demo   DemoOptions // used by PresetDemo
}

// Summary counts the rows in each table after seeding, in insert order.
type Summary []TableCount

// TableCount is the number of rows in one table.
type TableCount struct {
	Table string
	Rows  int64
}

// tables lists every seeded table in foreign-key order.
var tables = []string{
	"staff", "services", "customers", "locations", "tags", "customer_tags",
	"location_tags", "jobs", "documents", "document_items", "payments",
}

// Seed loads a preset in one transaction. Without Reset it refuses a database that
// already has data, so it can't silently mix datasets or collide with existing IDs.
func Seed(ctx context.Context, pool *pgxpool.Pool, opts SeedOptions) (Summary, error) {
	if opts.Preset != PresetScreenshot && opts.Preset != PresetDemo {
		return nil, fmt.Errorf("%w: %q, must be %s or %s", ErrUnknownPreset, opts.Preset, PresetScreenshot, PresetDemo)
	}
	var ds *Dataset
	if opts.Preset == PresetDemo {
		var err error
		if ds, err = Demo(opts.Demo); err != nil {
			return nil, err
		}
	}

	var summary Summary
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if !opts.Reset {
			if err := requireEmpty(ctx, tx); err != nil {
				return err
			}
		}
		// Also on an already empty database: rows removed with DELETE leave the ID counters
		// past 1, and the screenshot preset refers to its rows by those IDs.
		if _, err := tx.Exec(ctx, "TRUNCATE "+joinTables()+" RESTART IDENTITY CASCADE"); err != nil {
			return fmt.Errorf("reset tables: %w", err)
		}

		if ds == nil {
			if _, err := tx.Exec(ctx, ScreenshotSQL); err != nil {
				return fmt.Errorf("load screenshot preset: %w", err)
			}
		} else if err := insertDataset(ctx, tx, ds); err != nil {
			return err
		}

		var err error
		summary, err = countRows(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func joinTables() string {
	out := ""
	for i, t := range tables {
		if i > 0 {
			out += ", "
		}
		out += pgx.Identifier{t}.Sanitize()
	}
	return out
}

// requireEmpty fails with ErrNotEmpty when any seeded table has a row.
func requireEmpty(ctx context.Context, tx pgx.Tx) error {
	for _, t := range tables {
		var hasData bool
		err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+pgx.Identifier{t}.Sanitize()+")").Scan(&hasData)
		if err != nil {
			return fmt.Errorf("check %s for existing data: %w", t, err)
		}
		if hasData {
			return fmt.Errorf("%w: %s has rows, use reset to replace them", ErrNotEmpty, t)
		}
	}
	return nil
}

func countRows(ctx context.Context, tx pgx.Tx) (Summary, error) {
	summary := make(Summary, 0, len(tables))
	for _, t := range tables {
		var n int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", t, err)
		}
		summary = append(summary, TableCount{Table: t, Rows: n})
	}
	return summary, nil
}

// insertDataset bulk-loads ds with COPY. COPY writes the generated IDs into the identity
// columns, so each sequence is then moved past the highest ID for later inserts.
func insertDataset(ctx context.Context, tx pgx.Tx, ds *Dataset) error {
	type copySpec struct {
		table   string
		columns []string
		rows    [][]any
	}
	specs := []copySpec{
		{"staff", []string{"id", "name"}, rowsOf(ds.Staff, func(s Staff) []any { return []any{s.ID, s.Name} })},
		{"services", []string{"id", "name", "price", "is_recurring", "interval_days"}, rowsOf(ds.Services, func(s Service) []any {
			return []any{s.ID, s.Name, s.Price, s.IsRecurring, s.IntervalDays}
		})},
		{"customers", []string{"id", "name", "email", "status", "source", "created_at"}, rowsOf(ds.Customers, func(c Customer) []any {
			return []any{c.ID, c.Name, c.Email, c.Status, c.Source, c.CreatedAt}
		})},
		{"locations", []string{"id", "customer_id", "address", "city"}, rowsOf(ds.Locations, func(l Location) []any {
			return []any{l.ID, l.CustomerID, l.Address, l.City}
		})},
		{"tags", []string{"id", "name", "scope"}, rowsOf(ds.Tags, func(t Tag) []any { return []any{t.ID, t.Name, t.Scope} })},
		{"customer_tags", []string{"customer_id", "tag_id"}, rowsOf(ds.CustomerTags, func(ct CustomerTag) []any {
			return []any{ct.CustomerID, ct.TagID}
		})},
		{"location_tags", []string{"location_id", "tag_id"}, rowsOf(ds.LocationTags, func(lt LocationTag) []any {
			return []any{lt.LocationID, lt.TagID}
		})},
		{"jobs", []string{
			"id", "customer_id", "location_id", "service_id", "staff_id", "status",
			"booked_online", "total", "scheduled_at", "completed_at", "created_at",
		}, rowsOf(ds.Jobs, func(j Job) []any {
			return []any{
				j.ID, j.CustomerID, j.LocationID, j.ServiceID, j.StaffID, j.Status,
				j.BookedOnline, j.Total, j.ScheduledAt, j.CompletedAt, j.CreatedAt,
			}
		})},
		{"documents", []string{
			"id", "doc_type", "status", "number", "customer_id", "job_id", "source_document_id",
			"issued_at", "due_date", "expires_at", "subtotal", "tax", "total", "amount_paid",
		}, rowsOf(ds.Documents, func(d Document) []any {
			return []any{
				d.ID, d.DocType, d.Status, d.Number, d.CustomerID, d.JobID, d.SourceDocumentID,
				d.IssuedAt, d.DueDate, d.ExpiresAt, d.Subtotal, d.Tax, d.Total, d.AmountPaid,
			}
		})},
		{"document_items", []string{"id", "document_id", "service_id", "description", "quantity", "unit_price"}, rowsOf(ds.DocumentItems, func(i DocumentItem) []any {
			return []any{i.ID, i.DocumentID, i.ServiceID, i.Description, i.Quantity, i.UnitPrice}
		})},
		{"payments", []string{"id", "document_id", "method", "amount", "received_at"}, rowsOf(ds.Payments, func(p Payment) []any {
			return []any{p.ID, p.DocumentID, p.Method, p.Amount, p.ReceivedAt}
		})},
	}

	for _, s := range specs {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{s.table}, s.columns, pgx.CopyFromRows(s.rows)); err != nil {
			return fmt.Errorf("copy %s: %w", s.table, err)
		}
		if s.columns[0] != "id" {
			continue // join tables have no identity column
		}
		_, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, 'id'), coalesce(max(id), 0) + 1, false) FROM `+
			pgx.Identifier{s.table}.Sanitize(), s.table)
		if err != nil {
			return fmt.Errorf("advance %s id sequence: %w", s.table, err)
		}
	}
	return nil
}

func rowsOf[T any](items []T, row func(T) []any) [][]any {
	out := make([][]any, len(items))
	for i, item := range items {
		out[i] = row(item)
	}
	return out
}

// Today returns local midnight of the current day in loc, the default for DemoOptions.Today.
func Today(loc *time.Location) time.Time {
	y, m, d := time.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
