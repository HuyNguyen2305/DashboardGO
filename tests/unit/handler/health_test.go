package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/handler"
)

func init() { gin.SetMode(gin.TestMode) }

type fakePinger struct {
	err      error
	deadline bool
}

func (f *fakePinger) Ping(ctx context.Context) error {
	_, f.deadline = ctx.Deadline()
	return f.err
}

func newRouter(p handler.Pinger) *gin.Engine {
	return handler.NewRouter(handler.RouterDeps{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: handler.NewHealthHandler(p, time.Second),
	})
}

func get(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody))
	return w
}

func TestHealthCheck(t *testing.T) {
	cases := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantBody   string
	}{
		{"database up", nil, http.StatusOK, `{"status":"ok"}`},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePinger{err: tc.pingErr}
			w := get(t, newRouter(p), http.MethodGet, "/healthz")

			assert.Equal(t, tc.wantStatus, w.Code)
			assert.True(t, p.deadline, "ping must run with a timeout")
			if tc.wantBody != "" {
				assert.JSONEq(t, tc.wantBody, w.Body.String())
				return
			}
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "database unavailable", body.Error)
			assert.NotContains(t, w.Body.String(), "connection refused", "internal errors must not leak")
			assert.NotEmpty(t, body.RequestID)
		})
	}
}

func TestRouterFallbacks(t *testing.T) {
	cases := []struct {
		name, method, path string
		wantStatus         int
		wantError          string
	}{
		{"unknown path", http.MethodGet, "/nope", http.StatusNotFound, "not found"},
		{"wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, newRouter(&fakePinger{}), tc.method, tc.path)

			assert.Equal(t, tc.wantStatus, w.Code)
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.wantError, body.Error)
			assert.NotEmpty(t, body.RequestID)
		})
	}
}
