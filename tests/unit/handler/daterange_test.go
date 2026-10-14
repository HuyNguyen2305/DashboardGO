package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/middleware"
)

// rangeRouter exposes ParseRange and ParseYear on test routes, echoing the resolved range.
func rangeRouter(t *testing.T) *gin.Engine {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, loc)
	dates := daterange.NewResolver(loc, func() time.Time { return now })

	echo := func(parse func(*gin.Context, *daterange.Resolver) (daterange.Range, error)) gin.HandlerFunc {
		return func(c *gin.Context) {
			r, err := parse(c, dates)
			if err != nil {
				handler.WriteError(c, err)
				return
			}
			c.JSON(http.StatusOK, handler.NewDateRange(r))
		}
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/range", echo(handler.ParseRange))
	r.GET("/year", echo(handler.ParseYear))
	r.GET("/boom", func(c *gin.Context) { handler.WriteError(c, errors.New("pq: password leaked")) })
	return r
}

func TestParseRangeAndYear(t *testing.T) {
	r := rangeRouter(t)

	cases := []struct {
		name, url  string
		wantStatus int
		want       dto.DateRange
		wantErr    string
	}{
		{"defaults to this month", "/range", http.StatusOK, dto.DateRange{Preset: "this_month", Start: "2026-10-01", End: "2026-10-31"}, ""},
		{"preset", "/range?range=last_3_months", http.StatusOK, dto.DateRange{Preset: "last_3_months", Start: "2026-07-01", End: "2026-09-30"}, ""},
		{"empty range defaults to this month", "/range?range=", http.StatusOK, dto.DateRange{Preset: "this_month", Start: "2026-10-01", End: "2026-10-31"}, ""},
		{"custom from/to rejected", "/range?from=2026-09-13&to=2026-10-12", http.StatusBadRequest, dto.DateRange{}, "custom date ranges are not supported"},
		{"only from rejected", "/range?from=2026-10-01", http.StatusBadRequest, dto.DateRange{}, "custom date ranges are not supported"},
		{"empty to rejected", "/range?range=today&to=", http.StatusBadRequest, dto.DateRange{}, "custom date ranges are not supported"},
		{"custom is not a preset", "/range?range=custom", http.StatusBadRequest, dto.DateRange{}, "unknown date range"},
		{"unknown preset", "/range?range=yesterday", http.StatusBadRequest, dto.DateRange{}, "unknown date range"},
		{"year defaults to current", "/year", http.StatusOK, dto.DateRange{Preset: "year", Start: "2026-01-01", End: "2026-12-31"}, ""},
		{"empty year defaults to current", "/year?year=", http.StatusOK, dto.DateRange{Preset: "year", Start: "2026-01-01", End: "2026-12-31"}, ""},
		{"explicit year", "/year?year=2025", http.StatusOK, dto.DateRange{Preset: "year", Start: "2025-01-01", End: "2025-12-31"}, ""},
		{"year not a number", "/year?year=abc", http.StatusBadRequest, dto.DateRange{}, "year must be a whole number"},
		{"year out of bounds", "/year?year=1999", http.StatusBadRequest, dto.DateRange{}, "invalid year"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, http.NoBody))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantErr == "" {
				var got dto.DateRange
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				assert.Equal(t, tc.want, got)
				return
			}
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Contains(t, body.Error, tc.wantErr)
			assert.NotEmpty(t, body.RequestID)
		})
	}
}

func TestWriteErrorHidesInternalErrors(t *testing.T) {
	w := httptest.NewRecorder()
	rangeRouter(t).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", http.NoBody))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "internal server error", body.Error)
	assert.NotContains(t, w.Body.String(), "password")
}

func TestWriteErrorStatusCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{"query timeout", fmt.Errorf("dashboard stats: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, "request timed out"},
		{"client hung up", fmt.Errorf("dashboard stats: %w", context.Canceled), 499, "request canceled"},
		{"client input", fmt.Errorf("%w: 1999", daterange.ErrInvalidYear), http.StatusBadRequest, "invalid year: 1999"},
		{"anything else", errors.New("pq: password leaked"), http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RequestID())
			r.GET("/", func(c *gin.Context) { handler.WriteError(c, tc.err) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

			assert.Equal(t, tc.wantStatus, w.Code)
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.wantError, body.Error)
			assert.NotEmpty(t, body.RequestID)
		})
	}
}
