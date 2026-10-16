package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/model"
	"dashboardgo/tests/mocks"
)

func ptr[T any](v T) *T { return &v }

func TestRevenueByStaffEndpoint(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().RevenueByStaff(mock.Anything, presetIs(daterange.ThisMonth)).Return(model.StaffRevenue{
		Rows: []model.StaffRevenueRow{
			{StaffID: ptr(int64(1)), Name: "Thọ", AvatarURL: ptr("https://cdn.test/tho.png"), Count: 7, Amount: decimal.RequireFromString("955"), Percent: 68.2},
			{StaffID: nil, Name: "Other", Count: 4, Amount: decimal.RequireFromString("444.2"), Percent: 31.72},
		},
		TotalAmount: decimal.RequireFromString("1399.2"),
	}, nil).Once()

	code, body := call(t, svc, "/api/v1/dashboard/revenue-by-staff")
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{
		"range": {"preset": "this_month", "start": "2026-10-01", "end": "2026-10-31"},
		"total_amount": "1399.20",
		"rows": [
			{"staff_id": 1, "name": "Thọ", "avatar_url": "https://cdn.test/tho.png", "count": 7, "amount": "955.00", "percent": 68.2},
			{"staff_id": null, "name": "Other", "avatar_url": null, "count": 4, "amount": "444.20", "percent": 31.72}
		]
	}`, body)
}

func TestActiveServicesEndpoint(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().ActiveServices(mock.Anything, presetIs(daterange.Last7Days)).Return(model.ActiveServices{
		Rows: []model.ServiceRow{
			{ServiceID: ptr(int64(3)), Name: "Bi-Monthly Service", IsRecurring: true, Count: 4, RecurringCount: 4, CustomerCount: 2, Amount: decimal.RequireFromString("260"), Percent: 100},
			{ServiceID: nil, Name: "No service", Count: 1, Amount: decimal.Zero, Percent: 0},
		},
		TotalCount: 5, RecurringCount: 4, TotalAmount: decimal.RequireFromString("260"),
		ServiceCustomers: model.ServiceCustomers{CustomerCount: 3, RecurringCustomerCount: 2},
	}, nil).Once()

	code, body := call(t, svc, "/api/v1/dashboard/active-services?range=last_7_days")
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{
		"range": {"preset": "last_7_days", "start": "2026-10-06", "end": "2026-10-12"},
		"total_count": 5, "recurring_count": 4, "customer_count": 3, "recurring_customer_count": 2, "total_amount": "260.00",
		"rows": [
			{"service_id": 3, "name": "Bi-Monthly Service", "is_recurring": true, "count": 4, "recurring_count": 4, "customer_count": 2, "amount": "260.00", "percent": 100},
			{"service_id": null, "name": "No service", "is_recurring": false, "count": 1, "recurring_count": 0, "customer_count": 0, "amount": "0.00", "percent": 0}
		]
	}`, body)
}

func TestTagsEndpoint(t *testing.T) {
	usage := model.TagUsage{Rows: []model.TagRow{{TagID: ptr(int64(1)), Name: "1233", Count: 1, Percent: 50}}, TotalCount: 1}

	cases := []struct{ name, query, wantScope string }{
		{"defaults to location", "", "location"},
		{"empty scope defaults to location too", "?scope=", "location"},
		{"customer scope", "?scope=customer", "customer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := usage
			u.Scope = tc.wantScope
			svc := mocks.NewMockDashboardService(t)
			svc.EXPECT().Tags(mock.Anything, tc.wantScope).Return(u, nil).Once()

			code, body := call(t, svc, "/api/v1/dashboard/tags"+tc.query)
			require.Equal(t, http.StatusOK, code, body)
			assert.JSONEq(t, fmt.Sprintf(`{"scope": %q, "total_count": 1,
				"rows": [{"tag_id": 1, "name": "1233", "count": 1, "percent": 50}]}`, tc.wantScope), body)
		})
	}

	t.Run("invalid scope is a 400", func(t *testing.T) {
		svc := mocks.NewMockDashboardService(t)
		svc.EXPECT().Tags(mock.Anything, "staff").
			Return(model.TagUsage{}, fmt.Errorf("%w: %q", model.ErrInvalidScope, "staff")).Once()

		code, body := call(t, svc, "/api/v1/dashboard/tags?scope=staff")
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, body, "invalid tag scope")
	})

	t.Run("no tags encodes rows as []", func(t *testing.T) {
		svc := mocks.NewMockDashboardService(t)
		svc.EXPECT().Tags(mock.Anything, "location").Return(model.TagUsage{Scope: "location"}, nil).Once()

		code, body := call(t, svc, "/api/v1/dashboard/tags")
		require.Equal(t, http.StatusOK, code, body)
		assert.Contains(t, body, `"rows":[]`)
	})
}

func TestCustomersBySourceEndpoint(t *testing.T) {
	svc := mocks.NewMockDashboardService(t)
	svc.EXPECT().CustomersBySource(mock.Anything, mock.MatchedBy(func(r daterange.Range) bool {
		return r.Preset == daterange.Year && r.Start.Year() == 2026
	})).Return(model.SourceSeries{
		Sources: []string{"facebook", "na"},
		Months: []model.MonthlySourceCounts{
			{Month: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Counts: map[string]int64{"facebook": 1, "na": 10}},
		},
		TotalCount: 11,
	}, nil).Once()

	code, body := call(t, svc, "/api/v1/dashboard/customers-by-source")
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{
		"range": {"preset": "year", "start": "2026-01-01", "end": "2026-12-31"},
		"total_count": 11,
		"sources": ["facebook", "na"],
		"months": [{"month": "2026-10", "counts": {"facebook": 1, "na": 10}}]
	}`, body)

	t.Run("bad year", func(t *testing.T) {
		code, body := call(t, mocks.NewMockDashboardService(t), "/api/v1/dashboard/customers-by-source?year=abc")
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, body, "year must be a whole number")
	})
}

func TestSecondaryWidgetServiceErrorsAreHidden(t *testing.T) {
	boom := errors.New("pq: connection refused")
	cases := []struct {
		path   string
		expect func(*mocks.MockDashboardService)
	}{
		{"/api/v1/dashboard/revenue-by-staff", func(m *mocks.MockDashboardService) {
			m.EXPECT().RevenueByStaff(mock.Anything, mock.Anything).Return(model.StaffRevenue{}, boom).Once()
		}},
		{"/api/v1/dashboard/active-services", func(m *mocks.MockDashboardService) {
			m.EXPECT().ActiveServices(mock.Anything, mock.Anything).Return(model.ActiveServices{}, boom).Once()
		}},
		{"/api/v1/dashboard/tags", func(m *mocks.MockDashboardService) {
			m.EXPECT().Tags(mock.Anything, mock.Anything).Return(model.TagUsage{}, boom).Once()
		}},
		{"/api/v1/dashboard/customers-by-source", func(m *mocks.MockDashboardService) {
			m.EXPECT().CustomersBySource(mock.Anything, mock.Anything).Return(model.SourceSeries{}, boom).Once()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t)
			tc.expect(svc)
			code, body := call(t, svc, tc.path)
			assert.Equal(t, http.StatusInternalServerError, code)
			assert.NotContains(t, body, "connection refused")
		})
	}
}
