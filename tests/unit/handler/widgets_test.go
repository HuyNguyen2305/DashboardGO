package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/model"
	"dashboardgo/tests/mocks"
)

// call sends GET path to a router backed by svc and returns the status code and body.
func call(t *testing.T, svc *mocks.MockDashboardService, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	dashboardRouter(t, svc).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return w.Code, w.Body.String()
}

func TestJobsEndpoint(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().Jobs(mock.Anything, presetIs(daterange.ThisMonth), model.StaffFilter{IDs: []int64{3, 1, 2}}).Return(model.JobsWidget{
		Breakdown: model.Breakdown{
			Rows: []model.BreakdownRow{
				{Key: "incomplete", Count: 18, Amount: decimal.RequireFromString("1371.24"), Percent: 54.55},
				{Key: "completed", Count: 14, Amount: decimal.RequireFromString("924.3"), Percent: 42.42},
			},
			TotalCount:  32,
			TotalAmount: decimal.RequireFromString("2295.54"),
		},
		Daily: []model.DailyCounts{{
			Date:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			Counts: map[string]int64{"incomplete": 0, "completed": 1},
		}},
	}, nil).Once()

	// Repeated and comma-separated forms combine; duplicates are dropped.
	code, body := call(t, svc, "/api/v1/dashboard/jobs?staff_ids=3&staff_ids=1,2&staff_ids=3")

	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{
		"range": {"preset": "this_month", "start": "2026-10-01", "end": "2026-10-31"},
		"total_amount": "2295.54",
		"total_count": 32,
		"rows": [
			{"key": "incomplete", "count": 18, "amount": "1371.24", "percent": 54.55},
			{"key": "completed",  "count": 14, "amount": "924.30",  "percent": 42.42}
		],
		"daily": [{"date": "2026-10-01", "counts": {"incomplete": 0, "completed": 1}}]
	}`, body)
}

func TestJobsWithoutStaffFilterMeansAllSchedules(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().Jobs(mock.Anything, mock.Anything, model.StaffFilter{}).Return(model.JobsWidget{}, nil).Once()

	code, body := call(t, svc, "/api/v1/dashboard/jobs?range=today")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"rows":[]`, "empty rows must encode as [] not null")
}

func TestJobsStaffFilterAcceptsUnassigned(t *testing.T) {
	cases := []struct {
		name, query string
		want        model.StaffFilter
	}{
		{"only unassigned", "staff_ids=unassigned", model.StaffFilter{Unassigned: true}},
		{"staff and unassigned", "staff_ids=1,unassigned&staff_ids=2", model.StaffFilter{IDs: []int64{1, 2}, Unassigned: true}},
		{"unassigned twice", "staff_ids=unassigned,unassigned", model.StaffFilter{Unassigned: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t)
			svc.EXPECT().Jobs(mock.Anything, mock.Anything, tc.want).Return(model.JobsWidget{}, nil).Once()
			code, body := call(t, svc, "/api/v1/dashboard/jobs?"+tc.query)
			require.Equal(t, http.StatusOK, code, body)
		})
	}
}

func TestJobsRejectsBadStaffIDs(t *testing.T) {
	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("staff_ids=%d", i+1)
	}
	cases := []struct{ name, query, wantErr string }{
		{"not a number", "staff_ids=abc", "staff_ids must be positive whole numbers"},
		{"negative", "staff_ids=-1", "staff_ids must be positive whole numbers"},
		{"zero", "staff_ids=0", "staff_ids must be positive whole numbers"},
		{"misspelled unassigned", "staff_ids=Unassigned", `or "unassigned"`},
		{"too many", strings.Join(many, "&"), "at most 50 staff_ids"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t) // no expectations: must not be called
			code, body := call(t, svc, "/api/v1/dashboard/jobs?"+tc.query)

			assert.Equal(t, http.StatusBadRequest, code)
			var e dto.ErrorResponse
			require.NoError(t, json.Unmarshal([]byte(body), &e))
			assert.Contains(t, e.Error, tc.wantErr)
		})
	}
}

func TestBreakdownEndpoints(t *testing.T) {
	b := model.Breakdown{
		Rows:        []model.BreakdownRow{{Key: "check", Count: 11, Amount: decimal.RequireFromString("715"), Percent: 82.44}},
		TotalCount:  11,
		TotalAmount: decimal.RequireFromString("715"),
	}
	want := `{
		"range": {"preset": "last_month", "start": "2026-09-01", "end": "2026-09-30"},
		"total_amount": "715.00", "total_count": 11,
		"rows": [{"key": "check", "count": 11, "amount": "715.00", "percent": 82.44}]
	}`

	cases := []struct {
		path   string
		expect func(*mocks.MockDashboardService)
	}{
		{"/api/v1/dashboard/invoices", func(m *mocks.MockDashboardService) {
			m.EXPECT().Invoices(mock.Anything, presetIs(daterange.LastMonth)).Return(b, nil).Once()
		}},
		{"/api/v1/dashboard/estimates", func(m *mocks.MockDashboardService) {
			m.EXPECT().Estimates(mock.Anything, presetIs(daterange.LastMonth)).Return(b, nil).Once()
		}},
		{"/api/v1/dashboard/payments", func(m *mocks.MockDashboardService) {
			m.EXPECT().Payments(mock.Anything, presetIs(daterange.LastMonth)).Return(b, nil).Once()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t)
			tc.expect(svc)
			code, body := call(t, svc, tc.path+"?range=last_month")
			require.Equal(t, http.StatusOK, code, body)
			assert.JSONEq(t, want, body)
		})
	}
}

func TestInvoiceAgingEndpointUsesToday(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().InvoiceAging(mock.Anything, mock.MatchedBy(func(asOf time.Time) bool {
		return asOf.Format("2006-01-02 15:04 MST") == "2026-10-12 00:00 +07"
	})).Return(model.AgingWidget{
		AsOf: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Breakdown: model.Breakdown{
			Rows:        []model.BreakdownRow{{Key: "1_30", Count: 11, Amount: decimal.RequireFromString("647.91"), Percent: 87.21}},
			TotalCount:  11,
			TotalAmount: decimal.RequireFromString("647.91"),
		},
	}, nil).Once()

	// Aging takes no parameters: it is always as of today.
	code, body := call(t, svc, "/api/v1/dashboard/invoices/aging")
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{
		"as_of": "2026-10-12", "total_amount": "647.91", "total_count": 11,
		"rows": [{"key": "1_30", "count": 11, "amount": "647.91", "percent": 87.21}]
	}`, body)
}

func TestYearSeriesEndpoints(t *testing.T) {
	series := model.YearSeries{
		Months: []model.MonthlyAmount{
			{Month: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Amount: decimal.Zero},
			{Month: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), Amount: decimal.RequireFromString("12.5")},
		},
		Total: decimal.RequireFromString("12.5"),
	}
	isYear := func(y int) any {
		return mock.MatchedBy(func(r daterange.Range) bool { return r.Preset == daterange.Year && r.Start.Year() == y })
	}
	want := `{
		"range": {"preset": "year", "start": "2025-01-01", "end": "2025-12-31"},
		"total": "12.50",
		"months": [{"month": "2025-01", "amount": "0.00"}, {"month": "2025-02", "amount": "12.50"}]
	}`

	cases := []struct {
		path   string
		expect func(*mocks.MockDashboardService)
	}{
		{"/api/v1/dashboard/revenue-per-month", func(m *mocks.MockDashboardService) {
			m.EXPECT().RevenuePerMonth(mock.Anything, isYear(2025)).Return(series, nil).Once()
		}},
		{"/api/v1/dashboard/sales-forecast", func(m *mocks.MockDashboardService) {
			m.EXPECT().SalesForecast(mock.Anything, isYear(2025)).Return(series, nil).Once()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t)
			tc.expect(svc)
			code, body := call(t, svc, tc.path+"?year=2025")
			require.Equal(t, http.StatusOK, code, body)
			assert.JSONEq(t, want, body)
		})
	}

	t.Run("bad year", func(t *testing.T) {
		code, body := call(t, mocks.NewMockDashboardService(t), "/api/v1/dashboard/sales-forecast?year=1999")
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, body, "invalid year")
	})
	t.Run("default year", func(t *testing.T) {
		svc := mocks.NewMockDashboardService(t)
		svc.EXPECT().RevenuePerMonth(mock.Anything, isYear(2026)).Return(model.YearSeries{}, nil).Once()
		code, body := call(t, svc, "/api/v1/dashboard/revenue-per-month")
		assert.Equal(t, http.StatusOK, code, body)
	})
}

func TestWidgetServiceErrorsAreHidden(t *testing.T) {
	boom := errors.New("pq: canceling statement due to statement timeout")
	cases := []struct {
		path   string
		expect func(*mocks.MockDashboardService)
	}{
		{"/api/v1/dashboard/jobs", func(m *mocks.MockDashboardService) {
			m.EXPECT().Jobs(mock.Anything, mock.Anything, mock.Anything).Return(model.JobsWidget{}, boom).Once()
		}},
		{"/api/v1/dashboard/invoices", func(m *mocks.MockDashboardService) {
			m.EXPECT().Invoices(mock.Anything, mock.Anything).Return(model.Breakdown{}, boom).Once()
		}},
		{"/api/v1/dashboard/invoices/aging", func(m *mocks.MockDashboardService) {
			m.EXPECT().InvoiceAging(mock.Anything, mock.Anything).Return(model.AgingWidget{}, boom).Once()
		}},
		{"/api/v1/dashboard/sales-forecast", func(m *mocks.MockDashboardService) {
			m.EXPECT().SalesForecast(mock.Anything, mock.Anything).Return(model.YearSeries{}, boom).Once()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t)
			tc.expect(svc)
			code, body := call(t, svc, tc.path)
			assert.Equal(t, http.StatusInternalServerError, code)
			assert.NotContains(t, body, "statement timeout")
		})
	}
}
