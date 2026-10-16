//go:build integration

package recurring_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func itemDates(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["items"].([]any)
	require.True(t, ok, body)
	out := make([]string, len(raw))
	for i, it := range raw {
		out[i] = fmt.Sprintf("%v/%v", it.(map[string]any)["plan_id"], it.(map[string]any)["date"])
	}
	return out
}

func TestAPIProjections(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	cust := e.customer(t, "Ann")
	svc := e.service(t, "Lawn care", "50.00")
	weekly := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc})
	// Tuesdays, so the merge has to interleave with the Monday plan.
	tuesdays := e.seedPlan(t, planOpts{customerID: cust, serviceID: svc, start: e.date(2026, time.October, 6)})
	jobID := e.job(t, cust, weekly.ID, "2026-10-19", e.date(2026, time.October, 19).Add(9*time.Hour))
	window := "?from=2026-10-19&to=2026-11-01"
	base := fmt.Sprintf("%s/%d", plansPath, weekly.ID)

	// occurrences: both Mondays in the window, the linked one carries job_id
	code, occ := call(t, srv, http.MethodGet, base+"/occurrences"+window, nil)
	require.Equal(t, http.StatusOK, code, occ)
	assert.Equal(t, map[string]any{"preset": "custom", "start": "2026-10-19", "end": "2026-11-01"}, occ["range"])
	items := occ["items"].([]any)
	require.Len(t, items, 2)
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	assert.Equal(t, "2026-10-19", first["date"])
	assert.InDelta(t, float64(jobID), first["job_id"], 0)
	assert.Equal(t, "2026-10-26", second["date"])
	assert.Nil(t, second["job_id"])

	// projected invoices leave out the linked visit
	code, inv := call(t, srv, http.MethodGet, base+"/projected-invoices"+window, nil)
	require.Equal(t, http.StatusOK, code, inv)
	invItems := inv["items"].([]any)
	require.Len(t, invItems, 1)
	only := invItems[0].(map[string]any)
	assert.Equal(t, "2026-10-26", only["issued_on"])
	assert.Equal(t, "projected", only["kind"])
	assert.Equal(t, "50.00", only["total"])

	// the estimate still counts the linked visit
	code, est := call(t, srv, http.MethodGet, base+"/projected-estimate"+window, nil)
	require.Equal(t, http.StatusOK, code, est)
	assert.InDelta(t, 2, est["visit_count"], 0)
	assert.Equal(t, "100.00", est["total"])
	assert.Equal(t, "projected", est["kind"])

	// a window starting before today echoes the period actually priced, from today (Oct 15)
	code, est = call(t, srv, http.MethodGet, base+"/projected-estimate?from=2026-10-01&to=2026-10-31", nil)
	require.Equal(t, http.StatusOK, code, est)
	assert.Equal(t, map[string]any{"preset": "custom", "start": "2026-10-15", "end": "2026-10-31"}, est["range"])
	assert.InDelta(t, 2, est["visit_count"], 0, "Oct 19 and 26; earlier Mondays are past")

	// a window entirely in the past prices nothing and keeps the requested range
	code, est = call(t, srv, http.MethodGet, base+"/projected-estimate?from=2026-09-01&to=2026-09-30", nil)
	require.Equal(t, http.StatusOK, code, est)
	assert.Equal(t, map[string]any{"preset": "custom", "start": "2026-09-01", "end": "2026-09-30"}, est["range"])
	assert.InDelta(t, 0, est["visit_count"], 0)
	assert.Equal(t, "0.00", est["total"])

	// customer route merges both plans by date
	code, merged := call(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/recurring-occurrences%s", cust, window), nil)
	require.Equal(t, http.StatusOK, code, merged)
	assert.Equal(t, []string{
		fmt.Sprintf("%d/2026-10-19", weekly.ID), fmt.Sprintf("%d/2026-10-20", tuesdays.ID),
		fmt.Sprintf("%d/2026-10-26", weekly.ID), fmt.Sprintf("%d/2026-10-27", tuesdays.ID),
	}, itemDates(t, merged))

	// unknown customer, unknown plan, oversized window
	code, body := call(t, srv, http.MethodGet, "/api/v1/customers/999999/recurring-occurrences", nil)
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = call(t, srv, http.MethodGet, plansPath+"/999999/occurrences", nil)
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = call(t, srv, http.MethodGet, base+"/occurrences?from=2026-10-01&to=2040-01-01", nil)
	assert.Equal(t, http.StatusBadRequest, code, body)
}
