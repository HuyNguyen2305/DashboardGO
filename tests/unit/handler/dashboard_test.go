package handler_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/model"
	"dashboardgo/tests/mocks"
)

func dashboardRouter(t *testing.T, svc handler.DashboardService) *gin.Engine {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, loc)

	return mustRouter(t, handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(&fakePinger{}, time.Second),
		Dashboard: handler.NewDashboardHandler(svc, daterange.NewResolver(loc, func() time.Time { return now })),
	})
}

func getStats(t *testing.T, r *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/dashboard/stats"+query, http.NoBody))
	return w
}

func presetIs(p daterange.Preset) any {
	return mock.MatchedBy(func(r daterange.Range) bool { return r.Preset == p })
}

func TestStatsResponse(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().Stats(mock.Anything, presetIs(daterange.ThisMonth)).Return(model.DashboardStats{
		Revenue:             decimal.RequireFromString("1487.4"),
		Collected:           decimal.RequireFromString("867.26"),
		JobsCompleted:       14,
		NewJobsBookedOnline: decimal.Zero,
		TotalNewJobs:        decimal.RequireFromString("2110.54"),
	}, nil).Once()

	w := getStats(t, dashboardRouter(t, svc), "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"range": {"preset": "this_month", "start": "2026-10-01", "end": "2026-10-31"},
		"revenue": "1487.40",
		"collected": "867.26",
		"jobs_completed": 14,
		"new_jobs_booked_online": "0.00",
		"total_new_jobs": "2110.54"
	}`, w.Body.String())
}

func TestStatsForwardsRangeCappedAtNow(t *testing.T) {
	// The router's clock is frozen at 2026-10-12 10:00 GMT+7.
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().Stats(mock.Anything, mock.MatchedBy(func(r daterange.Range) bool {
		return r.Preset == daterange.ThisMonth &&
			r.Start.Format(time.DateTime) == "2026-10-01 00:00:00" &&
			r.End.Format(time.DateTime) == "2026-10-12 10:00:00"
	})).Return(model.DashboardStats{}, nil).Once()

	w := getStats(t, dashboardRouter(t, svc), "?range=this_month")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"end":"2026-10-31"`, "the response still echoes the whole period")
}

func TestStatsErrors(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		svcErr     error // nil means the service must not be called
		wantStatus int
		wantError  string
	}{
		{"bad range is rejected before the service", "?range=yesterday", nil, http.StatusBadRequest, "unknown date range"},
		{"service failure is hidden", "?range=today", errors.New("pq: relation does not exist"), http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The mock fails the test on any call that was not expected.
			svc := mocks.NewMockDashboardService(t)
			if tc.svcErr != nil {
				svc.EXPECT().Stats(mock.Anything, mock.Anything).Return(model.DashboardStats{}, tc.svcErr).Once()
			}

			w := getStats(t, dashboardRouter(t, svc), tc.query)

			assert.Equal(t, tc.wantStatus, w.Code)
			var body dto.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Contains(t, body.Error, tc.wantError)
			assert.NotContains(t, w.Body.String(), "relation does not exist")
		})
	}
}
