package handler_test

import (
	"bytes"
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

func mustRouter(t *testing.T, d handler.RouterDeps) *gin.Engine {
	t.Helper()
	r, err := handler.NewRouter(d)
	require.NoError(t, err)
	return r
}

func newRouter(t *testing.T, p handler.Pinger) *gin.Engine {
	t.Helper()
	return mustRouter(t, handler.RouterDeps{
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
			w := get(t, newRouter(t, p), http.MethodGet, "/healthz")

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
			w := get(t, newRouter(t, &fakePinger{}), tc.method, tc.path)

			assert.Equal(t, tc.wantStatus, w.Code)
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.wantError, body.Error)
			assert.NotEmpty(t, body.RequestID)
		})
	}
}

func TestHealthCheckAnswersHead(t *testing.T) {
	// Load balancers and uptime checkers often probe with HEAD.
	w := get(t, newRouter(t, &fakePinger{}), http.MethodHead, "/healthz")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestClientIPHonorsOnlyTrustedProxies(t *testing.T) {
	// httptest requests come from 192.0.2.1.
	cases := []struct {
		name    string
		proxies []string
		wantIP  string
	}{
		{"no trusted proxy ignores X-Forwarded-For", nil, "192.0.2.1"},
		{"untrusted peer ignores X-Forwarded-For", []string{"10.0.0.0/8"}, "192.0.2.1"},
		{"trusted peer uses X-Forwarded-For", []string{"192.0.2.0/24"}, "6.6.6.6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			r := mustRouter(t, handler.RouterDeps{
				Log:            slog.New(slog.NewTextHandler(&logs, nil)),
				TrustedProxies: tc.proxies,
				Health:         handler.NewHealthHandler(&fakePinger{}, time.Second),
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody)
			req.Header.Set("X-Forwarded-For", "6.6.6.6")
			r.ServeHTTP(httptest.NewRecorder(), req)

			assert.Contains(t, logs.String(), "client_ip="+tc.wantIP)
		})
	}
}

func TestNewRouterRejectsBadTrustedProxy(t *testing.T) {
	_, err := handler.NewRouter(handler.RouterDeps{
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		TrustedProxies: []string{"not-an-ip"},
		Health:         handler.NewHealthHandler(&fakePinger{}, time.Second),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trusted proxies")
}
