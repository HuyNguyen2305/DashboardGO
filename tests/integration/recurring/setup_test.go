//go:build integration

package recurring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/database"
	"dashboardgo/internal/daterange"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
	"dashboardgo/internal/repository"
	"dashboardgo/internal/service"
	"dashboardgo/tests/integration/testdb"
)

type env struct {
	pool *pgxpool.Pool
	repo *repository.RecurringRepository
	tz   *time.Location
}

// newEnv starts a migrated database with a business timezone of Asia/Ho_Chi_Minh.
func newEnv(t *testing.T) env {
	t.Helper()
	tz, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	pool, err := database.NewPool(context.Background(), database.PoolConfig{URL: testdb.StartMigrated(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return env{pool: pool, repo: repository.NewRecurringRepository(pool, 5*time.Second, tz), tz: tz}
}

// insertID runs an INSERT ... RETURNING id.
func (e env) insertID(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	require.NoError(t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&id))
	return id
}

func (e env) customer(t *testing.T, name string) int64 {
	t.Helper()
	return e.insertID(t, `INSERT INTO customers (name) VALUES ($1) RETURNING id`, name)
}

func (e env) location(t *testing.T, customerID int64) int64 {
	t.Helper()
	return e.insertID(t, `INSERT INTO locations (customer_id, address) VALUES ($1, '1 Main St') RETURNING id`, customerID)
}

func (e env) service(t *testing.T, name, price string) int64 {
	t.Helper()
	return e.insertID(t, `INSERT INTO services (name, price) VALUES ($1, $2) RETURNING id`, name, price)
}

func (e env) staff(t *testing.T, name string) int64 {
	t.Helper()
	return e.insertID(t, `INSERT INTO staff (name) VALUES ($1) RETURNING id`, name)
}

// date is local midnight in the business timezone.
func (e env) date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, e.tz)
}

type planOpts struct {
	customerID, serviceID int64
	start                 time.Time
	end                   *time.Time
	unit                  recurrence.Unit
	count                 int
}

// seedPlan creates a weekly plan through the repository; zero opts get defaults.
func (e env) seedPlan(t *testing.T, o planOpts) model.RecurringPlan {
	t.Helper()
	if o.unit == "" {
		o.unit, o.count = recurrence.Week, 1
	}
	if o.start.IsZero() {
		o.start = e.date(2026, time.October, 5)
	}
	price := decimal.RequireFromString("50.00")
	p, err := e.repo.CreatePlan(context.Background(), model.NewRecurringPlan{
		CustomerID: o.customerID, ServiceID: o.serviceID,
		Rule:      recurrence.Rule{Unit: o.unit, Count: o.count, Start: o.start, End: o.end},
		Quantity:  decimal.NewFromInt(1),
		UnitPrice: &price,
	})
	require.NoError(t, err)
	return p
}

func (e env) job(t *testing.T, customerID, planID int64, occurrence string, scheduled time.Time) int64 {
	t.Helper()
	return e.insertID(t, `
		INSERT INTO jobs (customer_id, recurring_plan_id, occurrence_date, scheduled_at)
		VALUES ($1, $2, $3::date, $4) RETURNING id`, customerID, planID, occurrence, scheduled)
}

// api serves the real router on e's database with the clock fixed at 2026-10-15 10:00 Asia/Ho_Chi_Minh.
func (e env) api(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, time.October, 15, 10, 0, 0, 0, e.tz)
	dates := daterange.NewResolver(e.tz, func() time.Time { return now })
	router, err := handler.NewRouter(handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(e.pool, time.Second),
		Recurring: handler.NewRecurringHandler(service.NewRecurringService(e.repo, dates), dates),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// call sends body (nil for none) as JSON and returns the status code and decoded JSON object.
// POST and PATCH carry Content-Type: application/json, which every write route requires.
func call(t *testing.T, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	contentType := ""
	if method == http.MethodPost || method == http.MethodPatch {
		contentType = "application/json"
	}
	return callRaw(t, srv, method, path, contentType, raw)
}

// callRaw sends body as is with contentType ("" sends no header) and decodes the JSON reply.
func callRaw(t *testing.T, srv *httptest.Server, method, path, contentType string, body []byte) (int, map[string]any) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rd)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return resp.StatusCode, out
}
