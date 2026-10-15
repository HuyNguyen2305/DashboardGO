package handler_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
	"dashboardgo/tests/mocks"
)

func hcm(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	return loc
}

// windowIs matches a Window by its exact From/To instants.
func windowIs(from, to time.Time) any {
	return mock.MatchedBy(func(w model.Window) bool { return w.From.Equal(from) && w.To.Equal(to) })
}

func TestOccurrencesDefaultsAndShape(t *testing.T) {
	loc := hcm(t)
	jobID := int64(99)
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Occurrences(mock.Anything, int64(3),
		windowIs(time.Date(2026, 10, 15, 0, 0, 0, 0, loc), time.Date(2027, 1, 15, 0, 0, 0, 0, loc))).
		Return([]model.PlanOccurrence{
			{PlanID: 3, Occurrence: recurrence.Occurrence{Index: 1, Date: time.Date(2026, 10, 19, 0, 0, 0, 0, loc)}, JobID: &jobID},
			{PlanID: 3, Occurrence: recurrence.Occurrence{Index: 2, Date: time.Date(2026, 10, 26, 0, 0, 0, 0, loc)}},
		}, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3/occurrences", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"range": {"preset": "custom", "start": "2026-10-15", "end": "2027-01-14"},
		"items": [
			{"plan_id": 3, "index": 1, "date": "2026-10-19", "job_id": 99},
			{"plan_id": 3, "index": 2, "date": "2026-10-26", "job_id": null}
		]
	}`, w.Body.String())
}

func TestOccurrencesExplicitToIsInclusive(t *testing.T) {
	loc := hcm(t)
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Occurrences(mock.Anything, int64(3),
		windowIs(time.Date(2026, 10, 1, 0, 0, 0, 0, loc), time.Date(2026, 11, 1, 0, 0, 0, 0, loc))).
		Return(nil, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3/occurrences?from=2026-10-01&to=2026-10-31", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"range": {"preset": "custom", "start": "2026-10-01", "end": "2026-10-31"}, "items": []}`, w.Body.String())
}

func TestOccurrencesSingleDayWindow(t *testing.T) {
	loc := hcm(t)
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Occurrences(mock.Anything, int64(3),
		windowIs(time.Date(2026, 10, 31, 0, 0, 0, 0, loc), time.Date(2026, 11, 1, 0, 0, 0, 0, loc))).
		Return(nil, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3/occurrences?from=2026-10-31&to=2026-10-31", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestProjectionRejectsBadWindow(t *testing.T) {
	paths := []string{
		"/api/v1/recurring-plans/3/occurrences",
		"/api/v1/recurring-plans/3/projected-invoices",
		"/api/v1/recurring-plans/3/projected-estimate",
		"/api/v1/customers/4/recurring-occurrences",
	}
	queries := []struct{ name, q, mentions string }{
		{"to before from", "?from=2026-11-01&to=2026-10-01", "to"},
		{"bad from", "?from=2026-13-01", "from"},
		{"bad to", "?to=nope", "to"},
		{"unknown key", "?month=2026-10", "month"},
	}
	for _, p := range paths {
		for _, tc := range queries {
			t.Run(p+" "+tc.name, func(t *testing.T) {
				w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodGet, p+tc.q, "")

				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), tc.mentions)
			})
		}
	}
}

func TestProjectionRejectsBadID(t *testing.T) {
	for _, p := range []string{
		"/api/v1/recurring-plans/abc/occurrences",
		"/api/v1/recurring-plans/0/projected-invoices",
		"/api/v1/recurring-plans/-1/projected-estimate",
		"/api/v1/customers/abc/recurring-occurrences",
	} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodGet, p, "")
		assert.Equal(t, http.StatusBadRequest, w.Code, p)
	}
}

func TestProjectedInvoicesShape(t *testing.T) {
	loc := hcm(t)
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().ProjectedInvoices(mock.Anything, int64(3),
		windowIs(time.Date(2026, 10, 15, 0, 0, 0, 0, loc), time.Date(2027, 1, 15, 0, 0, 0, 0, loc))).
		Return([]model.ProjectedInvoice{model.ProjectInvoice(samplePlan(t),
			recurrence.Occurrence{Index: 2, Date: time.Date(2026, 10, 19, 0, 0, 0, 0, loc)})}, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3/projected-invoices", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"range": {"preset": "custom", "start": "2026-10-15", "end": "2027-01-14"},
		"items": [{
			"plan_id": 3, "ref": "PRJ-3-2", "index": 2, "issued_on": "2026-10-19", "due_on": "2026-11-02",
			"line": {"description": "Lawn care", "quantity": "1.00", "unit_price": "50.00", "amount": "50.00"},
			"subtotal": "50.00", "tax": "5.00", "total": "55.00", "kind": "projected"
		}]
	}`, w.Body.String())
}

func TestProjectedEstimateDefaultsToTwelveMonths(t *testing.T) {
	loc := hcm(t)
	occs := []recurrence.Occurrence{
		{Index: 0, Date: time.Date(2026, 10, 5, 0, 0, 0, 0, loc)},
		{Index: 1, Date: time.Date(2026, 10, 19, 0, 0, 0, 0, loc)},
	}
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().ProjectedEstimate(mock.Anything, int64(3),
		windowIs(time.Date(2026, 10, 15, 0, 0, 0, 0, loc), time.Date(2027, 10, 15, 0, 0, 0, 0, loc))).
		Return(model.ProjectEstimate(samplePlan(t), occs, model.Window{}), nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3/projected-estimate", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"plan_id": 3,
		"range": {"preset": "custom", "start": "2026-10-15", "end": "2027-10-14"},
		"visit_count": 2,
		"line": {"description": "Lawn care", "quantity": "2.00", "unit_price": "50.00", "amount": "100.00"},
		"subtotal": "100.00", "tax": "10.00", "total": "110.00", "kind": "projected"
	}`, w.Body.String())
}

func TestCustomerOccurrences(t *testing.T) {
	loc := hcm(t)
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().CustomerOccurrences(mock.Anything, int64(4),
		windowIs(time.Date(2026, 10, 15, 0, 0, 0, 0, loc), time.Date(2027, 1, 15, 0, 0, 0, 0, loc))).
		Return([]model.PlanOccurrence{
			{PlanID: 5, Occurrence: recurrence.Occurrence{Index: 0, Date: time.Date(2026, 10, 20, 0, 0, 0, 0, loc)}},
		}, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/customers/4/recurring-occurrences", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"range": {"preset": "custom", "start": "2026-10-15", "end": "2027-01-14"},
		"items": [{"plan_id": 5, "index": 0, "date": "2026-10-20", "job_id": null}]
	}`, w.Body.String())
}

func TestProjectionErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		path string
		arm  func(*mocks.MockRecurringService, error)
		err  error
		code int
	}{
		{"customer not found", "/api/v1/customers/4/recurring-occurrences", func(s *mocks.MockRecurringService, err error) {
			s.EXPECT().CustomerOccurrences(mock.Anything, int64(4), mock.Anything).Return(nil, err).Once()
		}, model.ErrCustomerNotFound, http.StatusNotFound},
		{"plan not found", "/api/v1/recurring-plans/3/occurrences", func(s *mocks.MockRecurringService, err error) {
			s.EXPECT().Occurrences(mock.Anything, int64(3), mock.Anything).Return(nil, err).Once()
		}, model.ErrPlanNotFound, http.StatusNotFound},
		{"window too large", "/api/v1/recurring-plans/3/projected-invoices", func(s *mocks.MockRecurringService, err error) {
			s.EXPECT().ProjectedInvoices(mock.Anything, int64(3), mock.Anything).Return(nil, err).Once()
		}, model.ErrWindowTooLarge, http.StatusBadRequest},
		{"estimate plan not found", "/api/v1/recurring-plans/3/projected-estimate", func(s *mocks.MockRecurringService, err error) {
			s.EXPECT().ProjectedEstimate(mock.Anything, int64(3), mock.Anything).Return(model.ProjectedEstimate{}, err).Once()
		}, model.ErrPlanNotFound, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := mocks.NewMockRecurringService(t)
			tc.arm(svc, tc.err)

			w := do(t, recurringRouter(t, svc), http.MethodGet, tc.path, "")

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}
