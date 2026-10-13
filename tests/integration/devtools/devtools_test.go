//go:build integration

package devtools_test

import (
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/database"
	"dashboardgo/internal/daterange"
	"dashboardgo/internal/devdata"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/migrate"
	"dashboardgo/internal/repository"
	"dashboardgo/internal/service"
	"dashboardgo/tests/integration/testdb"
)

func TestMigrateRunner(t *testing.T) {
	m, err := migrate.New(testdb.Start(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	v, dirty, err := m.Version()
	require.NoError(t, err)
	assert.Zero(t, v, "a fresh database has no version")
	assert.False(t, dirty)

	require.NoError(t, m.Up())
	v, _, err = m.Version()
	require.NoError(t, err)
	assert.Equal(t, uint(3), v)

	assert.ErrorIs(t, m.Up(), migrate.ErrNoChange)

	require.NoError(t, m.Down(1))
	v, _, err = m.Version()
	require.NoError(t, err)
	assert.Equal(t, uint(2), v, "down without N reverts exactly one migration")

	require.Error(t, m.Down(0), "down 0 must be refused, never treated as 'all'")
	require.NoError(t, m.Up())
}

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := database.NewPool(context.Background(), database.PoolConfig{URL: testdb.StartMigrated(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestSeedRefusesExistingDataWithoutReset(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()

	_, err := devdata.Seed(ctx, pool, devdata.SeedOptions{Preset: devdata.PresetScreenshot})
	require.NoError(t, err)

	_, err = devdata.Seed(ctx, pool, devdata.SeedOptions{Preset: devdata.PresetScreenshot})
	require.ErrorIs(t, err, devdata.ErrNotEmpty)

	// --reset replaces the data; IDs restart so the screenshot preset (which expects ID 1) still loads.
	summary, err := devdata.Seed(ctx, pool, devdata.SeedOptions{Preset: devdata.PresetScreenshot, Reset: true})
	require.NoError(t, err)
	assert.Equal(t, devdata.TableCount{Table: "staff", Rows: 4}, summary[0])

	_, err = devdata.Seed(ctx, pool, devdata.SeedOptions{Preset: "everything"})
	require.ErrorIs(t, err, devdata.ErrUnknownPreset)
}

// TestDemoSeedServesConsistentDashboard loads generated data and checks that widgets
// computed by different queries agree with each other.
func TestDemoSeedServesConsistentDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := newPool(t)
	ctx := context.Background()

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	today := time.Date(2026, time.October, 12, 0, 0, 0, 0, loc)

	summary, err := devdata.Seed(ctx, pool, devdata.SeedOptions{
		Preset: devdata.PresetDemo,
		Demo:   devdata.DemoOptions{Months: 12, Seed: 42, Today: today},
	})
	require.NoError(t, err)
	for _, tc := range summary {
		if tc.Table != "location_tags" && tc.Table != "customer_tags" {
			assert.Positive(t, tc.Rows, tc.Table)
		}
	}

	// Rows inserted after seeding get fresh IDs: COPY must have moved the sequences forward.
	var newID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff (name) VALUES ('After Seed') RETURNING id`).Scan(&newID))
	assert.Equal(t, int64(5), newID)

	now := today.Add(10 * time.Hour)
	svc := service.NewDashboardService(repository.NewDashboardRepository(pool, 5*time.Second))
	srv := httptest.NewServer(handler.NewRouter(handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(pool, time.Second),
		Dashboard: handler.NewDashboardHandler(svc, daterange.NewResolver(loc, func() time.Time { return now })),
	}))
	t.Cleanup(srv.Close)

	get := func(path string, out any) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/dashboard/"+path, http.NoBody)
		require.NoError(t, err)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", path, body)
		require.NoError(t, json.Unmarshal(body, out), path)
	}

	for _, rng := range []string{"this_month", "last_6_months"} {
		t.Run(rng, func(t *testing.T) {
			var stats dto.StatsResponse
			get("stats?range="+rng, &stats)
			assert.NotEqual(t, "0.00", stats.Revenue, "demo data should produce revenue")

			var staff dto.StaffRevenueResponse
			get("revenue-by-staff?range="+rng, &staff)
			assert.Equal(t, stats.Revenue, staff.TotalAmount, "Revenue = Revenue by Staff total")

			var payments dto.BreakdownResponse
			get("payments?range="+rng, &payments)
			assert.Equal(t, stats.Collected, payments.TotalAmount, "Collected = Payments total")

			var jobs dto.JobsResponse
			get("jobs?range="+rng, &jobs)
			var services dto.ActiveServicesResponse
			get("active-services?range="+rng, &services)
			incomplete := jobs.Rows[0]
			require.Equal(t, "incomplete", incomplete.Key)
			assert.Equal(t, incomplete.Amount, services.TotalAmount, "Active Services = Jobs incomplete row")
			assert.Equal(t, incomplete.Count, services.TotalCount)

			var completed int64
			for _, r := range jobs.Rows {
				if r.Key == "completed" {
					completed = r.Count
				}
			}
			assert.Equal(t, stats.JobsCompleted, completed, "Jobs Completed = Jobs completed row")
		})
	}

	t.Run("yearly charts match the stats row for this month", func(t *testing.T) {
		var stats dto.StatsResponse
		get("stats", &stats)
		var revenue, forecast dto.YearSeriesResponse
		get("revenue-per-month", &revenue)
		get("sales-forecast", &forecast)
		assert.Equal(t, stats.Revenue, revenue.Months[9].Amount, "October revenue")
		assert.Equal(t, stats.TotalNewJobs, forecast.Months[9].Amount, "October forecast = Total New Jobs")
	})

	t.Run("remaining widgets respond", func(t *testing.T) {
		var aging dto.AgingResponse
		get("invoices/aging", &aging)
		assert.NotEqual(t, "0.00", aging.TotalAmount)

		var customers dto.CustomersBySourceResponse
		get("customers-by-source", &customers)
		assert.Positive(t, customers.TotalCount)

		for _, scope := range []string{"location", "customer"} {
			var tags dto.TagsResponse
			get("tags?scope="+scope, &tags)
			assert.NotEmpty(t, tags.Rows, scope)
		}

		var invoices, estimates dto.BreakdownResponse
		get("invoices", &invoices)
		get("estimates", &estimates)
		assert.Len(t, invoices.Rows, 8)
		assert.Len(t, estimates.Rows, 5)
	})
}
