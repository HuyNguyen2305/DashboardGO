//go:build integration

package dashboard_test

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
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/database"
	"dashboardgo/internal/daterange"
	"dashboardgo/internal/devdata"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/repository"
	"dashboardgo/internal/service"
	"dashboardgo/tests/integration/testdb"
)

type testEnv struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
}

// newEnv starts Postgres, applies migrations, loads devdata.ScreenshotSQL and serves the real router
// with the clock frozen at 2026-10-12 10:00 GMT+7.
func newEnv(t *testing.T) testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	pool, err := database.NewPool(ctx, database.PoolConfig{URL: testdb.StartMigrated(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, devdata.ScreenshotSQL)
	require.NoError(t, err)

	return testEnv{pool: pool}.at(t, time.Date(2026, time.October, 12, 10, 0, 0, 0, businessTZ(t)))
}

// at serves the real router on the same database with the clock frozen at now.
// Presets are the only date picker, so an empty range is reached by moving the clock.
func (e testEnv) at(t *testing.T, now time.Time) testEnv {
	t.Helper()
	dates := daterange.NewResolver(businessTZ(t), func() time.Time { return now })
	svc := service.NewDashboardService(repository.NewDashboardRepository(e.pool, 5*time.Second))
	router, err := handler.NewRouter(handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(e.pool, time.Second),
		Dashboard: handler.NewDashboardHandler(svc, dates),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return testEnv{srv: srv, pool: e.pool}
}

// before2026 is an env whose "last month" is January 2025, before any seeded row.
func (e testEnv) before2026(t *testing.T) testEnv {
	t.Helper()
	return e.at(t, time.Date(2025, time.February, 15, 10, 0, 0, 0, businessTZ(t)))
}

func businessTZ(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	return loc
}

// getStatus returns the status code and body of GET path.
func (e testEnv) getStatus(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.srv.URL+path, http.NoBody)
	require.NoError(t, err)
	resp, err := e.srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// getJSON requires a 200 from GET path and decodes the body into out.
func (e testEnv) getJSON(t *testing.T, path string, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.srv.URL+path, http.NoBody)
	require.NoError(t, err)
	resp, err := e.srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.NoError(t, json.Unmarshal(body, out))
}

// staffID looks up a seeded staff member's ID by name.
func (e testEnv) staffID(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT id FROM staff WHERE name = $1`, name).Scan(&id))
	return id
}
