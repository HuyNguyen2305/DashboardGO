//go:build integration

package recurring_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIWritesRejectNonJSONContentType(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	plan := e.seedPlan(t, planOpts{customerID: e.customer(t, "Ann"), serviceID: e.service(t, "Lawn care", "50.00")})
	path := fmt.Sprintf("%s/%d", plansPath, plan.ID)

	// A cross-site form can send text/plain without a preflight; it must not cancel the plan.
	code, body := callRaw(t, srv, http.MethodPost, path+"/cancel", "text/plain", []byte(`{}`))
	require.Equal(t, http.StatusUnsupportedMediaType, code, body)
	assert.Equal(t, "Content-Type must be application/json", body["error"])
	code, body = callRaw(t, srv, http.MethodPatch, path, "text/plain", []byte(`{"unit_price":"1.00"}`))
	require.Equal(t, http.StatusUnsupportedMediaType, code, body)

	code, got := call(t, srv, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, "active", got["status"], "the rejected cancel changed nothing")
	assert.Equal(t, "50.00", got["unit_price"], "the rejected patch changed nothing")

	code, body = callRaw(t, srv, http.MethodPost, path+"/pause", "application/json; charset=utf-8", nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "paused", body["status"])
}

func TestAPIBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	huge := []byte(`{"customer_id":1,"unit_price":"` + strings.Repeat("9", 70<<10) + `"}`)

	code, body := callRaw(t, srv, http.MethodPost, plansPath, "application/json", huge)
	require.Equal(t, http.StatusRequestEntityTooLarge, code, body)
	assert.Equal(t, "request body too large", body["error"])
}

func TestAPIEmptyPatchReturnsPlanUnchanged(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	plan := e.seedPlan(t, planOpts{customerID: e.customer(t, "Ann"), serviceID: e.service(t, "Lawn care", "50.00")})
	path := fmt.Sprintf("%s/%d", plansPath, plan.ID)
	_, before := call(t, srv, http.MethodGet, path, nil)

	for _, raw := range []string{`{}`, `null`} {
		code, body := callRaw(t, srv, http.MethodPatch, path, "application/json", []byte(raw))
		require.Equal(t, http.StatusOK, code, body)
		assert.Equal(t, before, body, "%s returns the stored plan, updated_at included", raw)
	}
}

func TestAPIPatchSameCadenceWithLinkedJob(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	cust := e.customer(t, "Ann")
	plan := e.seedPlan(t, planOpts{customerID: cust, serviceID: e.service(t, "Lawn care", "50.00")})
	e.job(t, cust, plan.ID, "2026-10-05", e.date(2026, 10, 5))

	// The full edit form re-sends the unchanged cadence alongside the real change.
	code, body := call(t, srv, http.MethodPatch, fmt.Sprintf("%s/%d", plansPath, plan.ID), map[string]any{
		"interval_unit": "week", "interval_count": 1, "start_date": "2026-10-05", "unit_price": "60.00",
	})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "60.00", body["unit_price"])
}

func TestAPIDefaultEstimateIsTwelveMonths(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	// Monthly from "today" (the fixed clock): 12 visits in the default 12-month window, not 13.
	plan := e.seedPlan(t, planOpts{
		customerID: e.customer(t, "Ann"), serviceID: e.service(t, "Lawn care", "50.00"),
		unit: "month", count: 1, start: e.date(2026, time.October, 15),
	})

	code, est := call(t, srv, http.MethodGet, fmt.Sprintf("%s/%d/projected-estimate", plansPath, plan.ID), nil)
	require.Equal(t, http.StatusOK, code, est)
	assert.InDelta(t, 12, est["visit_count"], 0)
	assert.Equal(t, map[string]any{"preset": "custom", "start": "2026-10-15", "end": "2027-10-14"}, est["range"])
}
