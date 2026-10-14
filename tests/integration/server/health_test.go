//go:build integration

package server_test

import (
	"context"
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
	"dashboardgo/internal/handler"
	"dashboardgo/tests/integration/testdb"
)

func TestHealthzAgainstRealPostgres(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	pool, err := database.NewPool(ctx, database.PoolConfig{URL: testdb.Start(t), MaxConns: 4, MinConns: 1})
	require.NoError(t, err)

	router, err := handler.NewRouter(handler.RouterDeps{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: handler.NewHealthHandler(pool, 2*time.Second),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// getHealthz returns the status code and request ID header of GET /healthz.
	getHealthz := func() (int, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/healthz", http.NoBody)
		require.NoError(t, err)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("X-Request-ID")
	}

	status, requestID := getHealthz()
	assert.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, requestID)

	// Once the pool is closed the check must report the database as unavailable.
	pool.Close()
	status, _ = getHealthz()
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestNewPoolRejectsUnreachableDatabase(t *testing.T) {
	_, err := database.NewPool(context.Background(), database.PoolConfig{
		URL:      "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		MaxConns: 1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping database")
}
