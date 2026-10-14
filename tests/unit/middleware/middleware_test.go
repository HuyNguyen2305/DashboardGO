package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/middleware"
)

func init() { gin.SetMode(gin.TestMode) }

func serve(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequestID(t *testing.T) {
	cases := []struct {
		name     string
		incoming string
		wantSame bool
	}{
		{"generates when missing", "", false},
		{"reuses valid incoming id", "abc-123_X.y", true},
		{"replaces id with unsafe characters", "bad id<script>", false},
		{"replaces overlong id", strings.Repeat("a", 65), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			r := gin.New()
			r.Use(middleware.RequestID())
			r.GET("/", func(c *gin.Context) { seen = middleware.GetRequestID(c) })

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tc.incoming != "" {
				req.Header.Set(middleware.RequestIDHeader, tc.incoming)
			}
			w := serve(r, req)

			got := w.Header().Get(middleware.RequestIDHeader)
			assert.Equal(t, seen, got, "context and header must carry the same id")
			if tc.wantSame {
				assert.Equal(t, tc.incoming, got)
			} else {
				assert.Regexp(t, `^[0-9a-f]{32}$`, got)
			}
		})
	}
}

func TestGetRequestIDWithoutMiddleware(t *testing.T) {
	var seen string
	r := gin.New()
	r.GET("/", func(c *gin.Context) { seen = middleware.GetRequestID(c) })
	serve(r, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	assert.Empty(t, seen)
}

func TestLoggerLevelByStatus(t *testing.T) {
	cases := []struct {
		status    int
		wantLevel string
	}{
		{http.StatusOK, "INFO"},
		{http.StatusNotFound, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))

			r := gin.New()
			r.Use(middleware.RequestID(), middleware.Logger(log))
			r.GET("/x", func(c *gin.Context) { c.Status(tc.status) })
			serve(r, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x?q=1", http.NoBody))

			var line map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
			assert.Equal(t, tc.wantLevel, line["level"])
			assert.Equal(t, "http request", line["msg"])
			assert.Equal(t, "GET", line["method"])
			assert.Equal(t, "/x", line["path"], "query string must not be logged")
			assert.InDelta(t, tc.status, line["status"], 0)
			assert.NotEmpty(t, line["request_id"])
		})
	}
}

func TestRecovery(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery(log))
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })

	w := serve(r, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", http.NoBody))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "internal server error", body.Error)
	assert.Equal(t, w.Header().Get(middleware.RequestIDHeader), body.RequestID)
	assert.NotContains(t, w.Body.String(), "kaboom", "panic details must not leak to clients")

	assert.Contains(t, buf.String(), "panic recovered")
	assert.Contains(t, buf.String(), "kaboom")
}

func TestCORS(t *testing.T) {
	const allowed = "http://app.test"
	cases := []struct {
		name         string
		method       string
		origin       string
		preflight    bool
		wantStatus   int
		wantAllowHdr string
	}{
		{"simple request from allowed origin", http.MethodGet, allowed, false, http.StatusOK, allowed},
		{"simple request from other origin", http.MethodGet, "http://evil.test", false, http.StatusOK, ""},
		{"request without origin", http.MethodGet, "", false, http.StatusOK, ""},
		{"preflight from allowed origin", http.MethodOptions, allowed, true, http.StatusNoContent, allowed},
		{"preflight from other origin", http.MethodOptions, "http://evil.test", true, http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.CORS([]string{allowed}))
			r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/", http.NoBody)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.preflight {
				req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			}
			w := serve(r, req)

			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantAllowHdr, w.Header().Get("Access-Control-Allow-Origin"))
			assert.Contains(t, w.Header().Values("Vary"), "Origin", "every response varies by Origin, so caches keep copies apart")
			if tc.preflight {
				assert.Contains(t, w.Header().Values("Vary"), "Access-Control-Request-Method")
			}
			if tc.wantStatus == http.StatusNoContent {
				assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), middleware.RequestIDHeader)
				assert.Equal(t, "GET, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
			}
		})
	}
}
