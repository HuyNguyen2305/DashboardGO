//go:build integration

package recurring_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const plansPath = "/api/v1/recurring-plans"

func planPath(body map[string]any, suffix string) string {
	return fmt.Sprintf("%s/%.0f%s", plansPath, body["id"], suffix)
}

func TestAPICRUDLifecycle(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	cust := e.customer(t, "Ann")
	svc := e.service(t, "Lawn care", "50.00")

	create := func(extra map[string]any) (int, map[string]any) {
		body := map[string]any{
			"customer_id": cust, "service_id": svc, "interval_unit": "week", "interval_count": 1,
			"start_date": "2026-10-05",
		}
		for k, v := range extra {
			body[k] = v
		}
		return call(t, srv, http.MethodPost, plansPath, body)
	}

	code, first := create(nil)
	require.Equal(t, http.StatusCreated, code, first)
	assert.Equal(t, "active", first["status"])
	assert.Equal(t, "50.00", first["unit_price"], "defaults to the service price")
	assert.Equal(t, "1.00", first["quantity"])
	assert.Equal(t, "0.0000", first["tax_rate"])
	assert.Equal(t, "Lawn care", first["service_name"])
	assert.Equal(t, "2026-10-05", first["start_date"])
	assert.Nil(t, first["end_date"])

	code, second := create(map[string]any{"unit_price": "65.50", "end_date": "2026-12-31"})
	require.Equal(t, http.StatusCreated, code, second)
	assert.Equal(t, "2026-12-31", second["end_date"])
	code, third := create(nil)
	require.Equal(t, http.StatusCreated, code, third)

	// get
	code, got := call(t, srv, http.MethodGet, planPath(first, ""), nil)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, first["id"], got["id"])

	// list with cursor paging: 3 plans, 2 per page
	code, page := call(t, srv, http.MethodGet, plansPath+"?limit=2", nil)
	require.Equal(t, http.StatusOK, code, page)
	assert.Len(t, page["items"], 2)
	require.NotNil(t, page["next_cursor"])
	code, page2 := call(t, srv, http.MethodGet, fmt.Sprintf("%s?limit=2&after=%.0f", plansPath, page["next_cursor"]), nil)
	require.Equal(t, http.StatusOK, code, page2)
	require.Len(t, page2["items"], 1)
	assert.Nil(t, page2["next_cursor"])
	assert.Equal(t, third["id"], page2["items"].([]any)[0].(map[string]any)["id"])

	// patch price and clear nothing else
	code, patched := call(t, srv, http.MethodPatch, planPath(first, ""), map[string]any{"unit_price": "75.00", "tax_rate": "0.1"})
	require.Equal(t, http.StatusOK, code, patched)
	assert.Equal(t, "75.00", patched["unit_price"])
	assert.Equal(t, "0.1000", patched["tax_rate"])

	// pause, resume, cancel, resume again
	code, body := call(t, srv, http.MethodPost, planPath(first, "/pause"), nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "paused", body["status"])
	code, body = call(t, srv, http.MethodPost, planPath(first, "/resume"), nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "active", body["status"])
	code, body = call(t, srv, http.MethodPost, planPath(first, "/cancel"), nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "canceled", body["status"])
	code, body = call(t, srv, http.MethodPost, planPath(first, "/resume"), nil)
	assert.Equal(t, http.StatusConflict, code, body)

	// status filter
	code, page = call(t, srv, http.MethodGet, plansPath+"?status=canceled", nil)
	require.Equal(t, http.StatusOK, code, page)
	assert.Len(t, page["items"], 1)
}

func TestAPIMissingPlanAndCustomer(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	svc := e.service(t, "Lawn care", "50.00")

	code, body := call(t, srv, http.MethodGet, plansPath+"/999", nil)
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = call(t, srv, http.MethodPost, plansPath+"/999/pause", nil)
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = call(t, srv, http.MethodPost, plansPath, map[string]any{
		"customer_id": 999, "service_id": svc, "interval_unit": "day", "interval_count": 1, "start_date": "2026-10-05",
	})
	assert.Equal(t, http.StatusBadRequest, code, "a missing customer is an invalid reference: "+fmt.Sprint(body))
}

func TestAPIPatchCadenceLockedByLinkedJob(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	cust := e.customer(t, "Ann")
	plan := e.seedPlan(t, planOpts{customerID: cust, serviceID: e.service(t, "Lawn care", "50.00")})
	path := fmt.Sprintf("%s/%d", plansPath, plan.ID)

	code, body := call(t, srv, http.MethodPatch, path, map[string]any{"interval_count": 2})
	require.Equal(t, http.StatusOK, code, "no linked jobs yet: "+fmt.Sprint(body))

	// Today is Oct 15: a job on a past visit can't be orphaned, since past visits are never projected.
	e.job(t, cust, plan.ID, "2026-10-05", e.date(2026, 10, 5))
	code, body = call(t, srv, http.MethodPatch, path, map[string]any{"interval_count": 3})
	require.Equal(t, http.StatusOK, code, "only past visits are booked: "+fmt.Sprint(body))

	// A job on an upcoming visit locks the cadence.
	e.job(t, cust, plan.ID, "2026-10-19", e.date(2026, 10, 19))
	code, body = call(t, srv, http.MethodPatch, path, map[string]any{"interval_count": 4})
	assert.Equal(t, http.StatusConflict, code, body)
	code, body = call(t, srv, http.MethodPatch, path, map[string]any{"unit_price": "60.00"})
	assert.Equal(t, http.StatusOK, code, "price stays editable: "+fmt.Sprint(body))
}

func TestAPIEndDateAndCancelKeepLinkedJobs(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	cust := e.customer(t, "Ann")
	// Weekly on Mondays from Oct 5; the Nov 2 visit is already booked.
	plan := e.seedPlan(t, planOpts{customerID: cust, serviceID: e.service(t, "Lawn care", "50.00")})
	jobID := e.job(t, cust, plan.ID, "2026-11-02", e.date(2026, 11, 2))
	path := fmt.Sprintf("%s/%d", plansPath, plan.ID)
	stillLinked := func(t *testing.T) {
		t.Helper()
		var planID int64
		var occurrence time.Time
		require.NoError(t, e.pool.QueryRow(context.Background(),
			`SELECT recurring_plan_id, occurrence_date FROM jobs WHERE id = $1`, jobID).Scan(&planID, &occurrence))
		assert.Equal(t, plan.ID, planID)
		assert.Equal(t, "2026-11-02", occurrence.Format(time.DateOnly))
	}

	// end_date is always editable, even before a booked visit.
	code, body := call(t, srv, http.MethodPatch, path, map[string]any{"end_date": "2026-10-31"})
	require.Equal(t, http.StatusOK, code, body)
	code, occ := call(t, srv, http.MethodGet, path+"/occurrences?from=2026-10-15&to=2026-11-30", nil)
	require.Equal(t, http.StatusOK, code, occ)
	assert.Equal(t, []string{fmt.Sprintf("%d/2026-10-19", plan.ID), fmt.Sprintf("%d/2026-10-26", plan.ID)},
		itemDates(t, occ), "the plan stops at the new end")
	stillLinked(t)

	// Canceling leaves the booked job alone too.
	code, body = call(t, srv, http.MethodPost, path+"/cancel", map[string]any{})
	require.Equal(t, http.StatusOK, code, body)
	stillLinked(t)
}

func TestAPICrossCustomerLocationRejected(t *testing.T) {
	e := newEnv(t)
	srv := e.api(t)
	ann, bob := e.customer(t, "Ann"), e.customer(t, "Bob")
	svc := e.service(t, "Lawn care", "50.00")
	bobsLocation := e.location(t, bob)

	code, body := call(t, srv, http.MethodPost, plansPath, map[string]any{
		"customer_id": ann, "location_id": bobsLocation, "service_id": svc,
		"interval_unit": "day", "interval_count": 1, "start_date": "2026-10-05",
	})
	assert.Equal(t, http.StatusBadRequest, code, body)
	assertFieldReason(t, body)

	plan := e.seedPlan(t, planOpts{customerID: ann, serviceID: svc})
	code, body = call(t, srv, http.MethodPatch, fmt.Sprintf("%s/%d", plansPath, plan.ID), map[string]any{"location_id": bobsLocation})
	assert.Equal(t, http.StatusBadRequest, code, body)
	assertFieldReason(t, body)
}

// assertFieldReason checks a cross-customer location error names the field, not the constraint.
func assertFieldReason(t *testing.T, body map[string]any) {
	t.Helper()
	msg, _ := body["error"].(string)
	assert.Equal(t, "invalid reference: location_id does not belong to the customer", msg)
	assert.NotContains(t, msg, "fkey")
	assert.NotContains(t, msg, "constraint")
}
