//go:build integration

package server_test

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/database"
	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/repository"
	"dashboardgo/internal/service"
	"dashboardgo/tests/integration/testdb"
)

func TestQueryTimeoutReturns504(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	pool, err := database.NewPool(ctx, database.PoolConfig{URL: testdb.StartMigrated(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	// A 1ns query timeout expires before Postgres can answer.
	svc := service.NewDashboardService(repository.NewDashboardRepository(pool, time.Nanosecond))
	router, err := handler.NewRouter(handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(pool, time.Second),
		Dashboard: handler.NewDashboardHandler(svc, daterange.NewResolver(loc, time.Now)),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/dashboard/stats", http.NoBody)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	var body dto.ErrorResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "request timed out", body.Error)
	assert.NotEmpty(t, body.RequestID)
}
