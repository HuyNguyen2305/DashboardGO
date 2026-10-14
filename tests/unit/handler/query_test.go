package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
	"dashboardgo/tests/mocks"
)

func TestDashboardRoutesRejectUnknownQueryParams(t *testing.T) {
	cases := []struct {
		name, path, wantErr string
	}{
		{"bracketed staff_ids", "/api/v1/dashboard/jobs?staff_ids[]=1", `unknown query parameter "staff_ids[]"; allowed: range, staff_ids`},
		{"typo of staff_ids", "/api/v1/dashboard/jobs?staff_id=1", `unknown query parameter "staff_id"`},
		{"typo of range", "/api/v1/dashboard/stats?rang=today", `unknown query parameter "rang"; allowed: range`},
		{"several unknown, sorted", "/api/v1/dashboard/stats?to=2026-10-02&from=2026-10-01", `unknown query parameter "from", "to"`},
		{"aging takes none", "/api/v1/dashboard/invoices/aging?range=last_month", `unknown query parameter "range"; allowed: none`},
		{"range on a yearly chart", "/api/v1/dashboard/revenue-per-month?range=this_year", `allowed: year`},
		{"year on a period widget", "/api/v1/dashboard/payments?year=2026", `allowed: range`},
		{"tags take scope only", "/api/v1/dashboard/tags?scope=location&range=today", `unknown query parameter "range"; allowed: scope`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := mocks.NewMockDashboardService(t) // no expectations: must not be called
			code, body := call(t, svc, tc.path)

			require.Equal(t, http.StatusBadRequest, code, body)
			var e dto.ErrorResponse
			require.NoError(t, json.Unmarshal([]byte(body), &e))
			assert.Contains(t, e.Error, tc.wantErr)
			assert.NotEmpty(t, e.RequestID)
		})
	}
}

func TestHealthzIgnoresQueryParams(t *testing.T) {
	// Probes sometimes add cache-busting parameters; health checks must not fail on them.
	w := get(t, newRouter(t, &fakePinger{}), http.MethodGet, "/healthz?probe=1")
	assert.Equal(t, http.StatusOK, w.Code)
}
