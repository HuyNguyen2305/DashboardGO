package handler_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/handler"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
	"dashboardgo/tests/mocks"
)

func recurringRouter(t *testing.T, svc handler.RecurringService) *gin.Engine {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	now := time.Date(2026, time.October, 15, 10, 0, 0, 0, loc)
	return mustRouter(t, handler.RouterDeps{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health:    handler.NewHealthHandler(&fakePinger{}, time.Second),
		Recurring: handler.NewRecurringHandler(svc, daterange.NewResolver(loc, func() time.Time { return now })),
	})
}

func do(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if method == http.MethodPost || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	return w
}

func samplePlan(t *testing.T) model.RecurringPlan {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	locationID := int64(7)
	return model.RecurringPlan{
		ID: 3, CustomerID: 1, ServiceID: 2, LocationID: &locationID, ServiceName: "Lawn care",
		Status:           model.PlanStatusActive,
		Rule:             recurrence.Rule{Unit: recurrence.Week, Count: 2, Start: time.Date(2026, 10, 5, 0, 0, 0, 0, loc)},
		Quantity:         decimal.NewFromInt(1),
		UnitPrice:        decimal.RequireFromString("50"),
		TaxRate:          decimal.RequireFromString("0.1"),
		PaymentTermsDays: 14,
		CreatedAt:        time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC),
	}
}

const validCreate = `{"customer_id":1,"service_id":2,"interval_unit":"week","interval_count":2,"start_date":"2026-10-05"}`

func TestRecurringCreateResponse(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Create(mock.Anything, mock.MatchedBy(func(in model.NewRecurringPlan) bool {
		return in.CustomerID == 1 && in.ServiceID == 2 && in.Rule.Unit == recurrence.Week && in.Rule.Count == 2 &&
			in.Rule.Start.Format(time.DateTime) == "2026-10-05 00:00:00" && in.Rule.End == nil &&
			in.Quantity.Equal(decimal.NewFromInt(1)) && in.UnitPrice == nil && in.TaxRate.IsZero()
	})).Return(samplePlan(t), nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodPost, "/api/v1/recurring-plans", validCreate)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"id": 3, "customer_id": 1, "location_id": 7, "service_id": 2, "service_name": "Lawn care",
		"staff_id": null, "status": "active", "interval_unit": "week", "interval_count": 2,
		"start_date": "2026-10-05", "end_date": null,
		"quantity": "1.00", "unit_price": "50.00", "tax_rate": "0.1000", "payment_terms_days": 14,
		"created_at": "2026-10-15T03:00:00Z", "updated_at": "2026-10-15T03:00:00Z"
	}`, w.Body.String())
}

func TestRecurringCreateAcceptsTrailingZeros(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Create(mock.Anything, mock.MatchedBy(func(in model.NewRecurringPlan) bool {
		return in.UnitPrice != nil && in.UnitPrice.Equal(decimal.RequireFromString("10.5"))
	})).Return(samplePlan(t), nil).Once()

	body := `{"customer_id":1,"service_id":2,"interval_unit":"day","interval_count":1,"start_date":"2026-10-05","unit_price":"10.500"}`
	w := do(t, recurringRouter(t, svc), http.MethodPost, "/api/v1/recurring-plans", body)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestRecurringCreateRejectsBadBody(t *testing.T) {
	tests := []struct {
		name, extra, field string
	}{
		{"price too precise", `"unit_price":"10.005"`, "unit_price"},
		{"price not a number", `"unit_price":"abc"`, "unit_price"},
		{"price negative", `"unit_price":"-1"`, "unit_price"},
		{"quantity too precise", `"quantity":"1.234"`, "quantity"},
		{"quantity zero", `"quantity":"0"`, "quantity"},
		{"quantity negative", `"quantity":"-1"`, "quantity"},
		{"tax too precise", `"tax_rate":"0.12345"`, "tax_rate"},
		{"tax above one", `"tax_rate":"1.5"`, "tax_rate"},
		{"tax negative", `"tax_rate":"-0.1"`, "tax_rate"},
		{"unit year", `"interval_unit":"year"`, "interval_unit"},
		{"bad month", `"start_date":"2026-13-01"`, "start_date"},
		{"bad end", `"end_date":"nope"`, "end_date"},
		{"count zero", `"interval_count":0`, "interval_count"},
		{"terms too long", `"payment_terms_days":366`, "payment_terms_days"},
		{"unknown key", `"colour":"red"`, "colour"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Later duplicate keys win, so the bad value replaces the valid one.
			body := strings.TrimSuffix(validCreate, "}") + "," + tc.extra + "}"
			w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPost, "/api/v1/recurring-plans", body)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.field)
		})
	}
}

func TestRecurringCreateRejectsMalformedJSON(t *testing.T) {
	for _, body := range []string{``, `{`, validCreate + `{}`} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPost, "/api/v1/recurring-plans", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, body)
	}
}

func TestRecurringPatchRejectsImmutableFields(t *testing.T) {
	for _, field := range []string{"customer_id", "service_id", "status"} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPatch, "/api/v1/recurring-plans/3",
			fmt.Sprintf(`{%q:1}`, field))
		require.Equal(t, http.StatusBadRequest, w.Code, field)
		assert.Contains(t, w.Body.String(), field)
	}
}

func TestRecurringPatchNullClearsLocation(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Update(mock.Anything, int64(3), mock.MatchedBy(func(p model.RecurringPlanPatch) bool {
		return p.LocationID.Set && p.LocationID.Value == nil && !p.StaffID.Set && !p.EndDate.Set && !p.Quantity.Set
	})).Return(samplePlan(t), nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodPatch, "/api/v1/recurring-plans/3", `{"location_id": null}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRecurringPatchConvertsFields(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Update(mock.Anything, int64(3), mock.MatchedBy(func(p model.RecurringPlanPatch) bool {
		return p.StaffID.Set && *p.StaffID.Value == 4 &&
			p.EndDate.Set && p.EndDate.Value != nil && p.EndDate.Value.Format(time.DateTime) == "2026-12-31 00:00:00" &&
			p.UnitPrice.Set && p.UnitPrice.Value.Equal(decimal.RequireFromString("12.5")) &&
			p.IntervalUnit.Set && p.IntervalUnit.Value == recurrence.Month &&
			p.PaymentTermsDays.Set && p.PaymentTermsDays.Value == 30
	})).Return(samplePlan(t), nil).Once()

	body := `{"staff_id":4,"end_date":"2026-12-31","unit_price":"12.50","interval_unit":"month","payment_terms_days":30}`
	w := do(t, recurringRouter(t, svc), http.MethodPatch, "/api/v1/recurring-plans/3", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRecurringPatchRejectsBadValues(t *testing.T) {
	for field, body := range map[string]string{
		"unit_price":         `{"unit_price":"10.005"}`,
		"interval_unit":      `{"interval_unit":"year"}`,
		"interval_count":     `{"interval_count":0}`,
		"start_date":         `{"start_date":"2026-13-01"}`,
		"end_date":           `{"end_date":"x"}`,
		"payment_terms_days": `{"payment_terms_days":-1}`,
		"location_id":        `{"location_id":0}`,
	} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPatch, "/api/v1/recurring-plans/3", body)
		require.Equal(t, http.StatusBadRequest, w.Code, field)
		assert.Contains(t, w.Body.String(), field)
	}
}

func TestRecurringListQuery(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	next := int64(9)
	svc.EXPECT().List(mock.Anything, mock.MatchedBy(func(q model.PlanListQuery) bool {
		return q.CustomerID != nil && *q.CustomerID == 1 && q.Status != nil && *q.Status == "paused" && q.Limit == 2 && q.After == 5
	})).Return(model.PlanPage{Items: []model.RecurringPlan{samplePlan(t)}, NextCursor: &next}, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans?customer_id=1&status=paused&limit=2&after=5", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"next_cursor":9`)
	assert.Contains(t, w.Body.String(), `"items":[{"id":3`)
}

func TestRecurringListEmptyPage(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().List(mock.Anything, model.PlanListQuery{}).Return(model.PlanPage{}, nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans", "")

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items":[],"next_cursor":null}`, w.Body.String())
}

func TestRecurringRejectsBadQueries(t *testing.T) {
	for _, q := range []string{
		"?limit=0", "?limit=x", "?limit=201", "?after=x", "?after=-1", "?limit=0&after=x",
		"?customer_id=0", "?customer_id=x", "?status=gone", "?colour=red",
	} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodGet, "/api/v1/recurring-plans"+q, "")
		assert.Equal(t, http.StatusBadRequest, w.Code, q)
	}
	w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodGet, "/api/v1/recurring-plans/3?x=1", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecurringRejectsBadID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc"} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodGet, "/api/v1/recurring-plans/"+id, "")
		require.Equal(t, http.StatusBadRequest, w.Code, id)
		assert.Contains(t, w.Body.String(), "invalid plan id")
	}
}

func TestRecurringTransitions(t *testing.T) {
	for path, action := range map[string]model.PlanAction{
		"pause": model.PlanPause, "resume": model.PlanResume, "cancel": model.PlanCancel,
	} {
		svc := mocks.NewMockRecurringService(t)
		svc.EXPECT().Transition(mock.Anything, int64(3), action).Return(samplePlan(t), nil).Once()

		w := do(t, recurringRouter(t, svc), http.MethodPost, "/api/v1/recurring-plans/3/"+path, "")

		require.Equal(t, http.StatusOK, w.Code, path)
		assert.Contains(t, w.Body.String(), `"id":3`)
	}
}

func TestRecurringErrorMapping(t *testing.T) {
	tests := []struct {
		err  error
		code int
	}{
		{model.ErrPlanNotFound, http.StatusNotFound},
		{model.ErrCustomerNotFound, http.StatusNotFound},
		{model.ErrCadenceLocked, http.StatusConflict},
		{model.ErrInvalidTransition, http.StatusConflict},
		{model.ErrPlanCanceled, http.StatusConflict},
		{model.ErrInvalidReference, http.StatusBadRequest},
		{model.ErrInvalidPlan, http.StatusBadRequest},
		{model.ErrWindowTooLarge, http.StatusBadRequest},
		{recurrence.ErrInvalidRule, http.StatusBadRequest},
		{errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			svc := mocks.NewMockRecurringService(t)
			svc.EXPECT().Get(mock.Anything, int64(3)).Return(model.RecurringPlan{}, fmt.Errorf("get: %w", tc.err)).Once()

			w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3", "")

			assert.Equal(t, tc.code, w.Code)
		})
	}
}

func TestRecurringRejectsOutOfRangeAmounts(t *testing.T) {
	for _, extra := range []string{
		`"unit_price":"10000000000"`, `"unit_price":"1e20"`, `"quantity":"100000000"`, `"quantity":"1e20"`,
	} {
		body := strings.TrimSuffix(validCreate, "}") + "," + extra + "}"
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPost, "/api/v1/recurring-plans", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, extra)
	}
	w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPatch, "/api/v1/recurring-plans/3", `{"unit_price":"10000000000"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecurringAcceptsLargestAmounts(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Create(mock.Anything, mock.Anything).Return(samplePlan(t), nil).Once()
	body := strings.TrimSuffix(validCreate, "}") + `,"unit_price":"9999999999.99","quantity":"99999999.99"}`

	w := do(t, recurringRouter(t, svc), http.MethodPost, "/api/v1/recurring-plans", body)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestRecurringPatchRejectsNullForNonNullableFields(t *testing.T) {
	for _, field := range []string{
		"interval_unit", "interval_count", "start_date", "quantity", "unit_price", "tax_rate", "payment_terms_days",
	} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPatch, "/api/v1/recurring-plans/3",
			fmt.Sprintf(`{%q:null}`, field))
		require.Equal(t, http.StatusBadRequest, w.Code, field)
		assert.Contains(t, w.Body.String(), field+": must not be null")
	}
}

func TestRecurringPatchNullEndDateClears(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Update(mock.Anything, int64(3), mock.MatchedBy(func(p model.RecurringPlanPatch) bool {
		return p.EndDate.Set && p.EndDate.Value == nil
	})).Return(samplePlan(t), nil).Once()

	w := do(t, recurringRouter(t, svc), http.MethodPatch, "/api/v1/recurring-plans/3", `{"end_date":null}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRecurringPatchTypeErrorsNameTheField(t *testing.T) {
	for field, body := range map[string]string{
		"unit_price":     `{"unit_price": 10.5}`,
		"interval_count": `{"interval_count": "x"}`,
		"location_id":    `{"location_id": "7"}`,
	} {
		w := do(t, recurringRouter(t, mocks.NewMockRecurringService(t)), http.MethodPatch, "/api/v1/recurring-plans/3", body)
		require.Equal(t, http.StatusBadRequest, w.Code, field)
		assert.Contains(t, w.Body.String(), field)
	}
}
