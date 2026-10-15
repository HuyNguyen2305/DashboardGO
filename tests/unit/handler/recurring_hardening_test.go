package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/model"
	"dashboardgo/tests/mocks"
)

// doType sends body with an explicit Content-Type header ("" sends none).
func doType(t *testing.T, svc *mocks.MockRecurringService, method, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	recurringRouter(t, svc).ServeHTTP(w, req)
	return w
}

func errorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return resp.Error
}

func TestRecurringErrorMessagesStripWrapPrefixes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"repeated prefixes", fmt.Errorf("get plan: get plan: %w", model.ErrPlanNotFound), http.StatusNotFound, "recurring plan not found"},
		{
			"detail after the sentinel is kept",
			fmt.Errorf("create plan: %w: location_id does not belong to the customer", model.ErrInvalidReference),
			http.StatusBadRequest, "invalid reference: location_id does not belong to the customer",
		},
		{
			"joined rollback error is cut at the newline",
			errors.Join(fmt.Errorf("lock plan: %w", model.ErrPlanNotFound), errors.New("rollback: conn busy on 10.0.0.1")),
			http.StatusNotFound, "recurring plan not found",
		},
		{"conflict", fmt.Errorf("update plan 3: %w", model.ErrCadenceLocked), http.StatusConflict, "plan cadence is locked by linked jobs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := mocks.NewMockRecurringService(t)
			svc.EXPECT().Get(mock.Anything, int64(3)).Return(model.RecurringPlan{}, tc.err).Once()

			w := do(t, recurringRouter(t, svc), http.MethodGet, "/api/v1/recurring-plans/3", "")

			require.Equal(t, tc.code, w.Code)
			assert.Equal(t, tc.want, errorBody(t, w))
		})
	}
}

func TestRecurringBodyTooLarge(t *testing.T) {
	huge := `{"customer_id":1,"service_id":2,"interval_unit":"week","interval_count":2,"start_date":"2026-10-05","unit_price":"` +
		strings.Repeat("1", 70<<10) + `"}`
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		path := "/api/v1/recurring-plans"
		if method == http.MethodPatch {
			path += "/3"
		}
		w := doType(t, mocks.NewMockRecurringService(t), method, path, "application/json", huge)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, method)
		assert.Equal(t, "request body too large", errorBody(t, w), method)
	}
}

func TestRecurringDecodeErrorMessages(t *testing.T) {
	tests := []struct {
		name, method, body, want string
	}{
		{"create empty body", http.MethodPost, ``, "invalid request body: request body is required"},
		{"patch empty body", http.MethodPatch, ``, "invalid request body: request body is required"},
		{"create syntax error", http.MethodPost, `{"customer_id":1,}`, "invalid request body: malformed JSON"},
		{"create truncated", http.MethodPost, `{"customer_id":1`, "invalid request body: malformed JSON"},
		{"patch syntax error", http.MethodPatch, `{"unit_price":}`, "invalid request body: malformed JSON"},
		{
			"create wrong type", http.MethodPost,
			`{"customer_id":"secret-value","service_id":2,"interval_unit":"week","interval_count":2,"start_date":"2026-10-05"}`,
			"invalid request body: customer_id: must be a number",
		},
		{"create wrong top-level type", http.MethodPost, `[1]`, "invalid request body: request body: must be an object"},
		{"patch wrong type string", http.MethodPatch, `{"unit_price": 12345.678}`, "invalid request body: unit_price: must be a string"},
		{"patch wrong type number", http.MethodPatch, `{"interval_count": "secret-value"}`, "invalid request body: interval_count: must be a number"},
		{"patch wrong type id", http.MethodPatch, `{"location_id": {"x":1}}`, "invalid request body: location_id: must be a number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/v1/recurring-plans"
			if tc.method == http.MethodPatch {
				path += "/3"
			}
			w := doType(t, mocks.NewMockRecurringService(t), tc.method, path, "application/json", tc.body)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			msg := errorBody(t, w)
			assert.Equal(t, tc.want, msg)
			for _, leak := range []string{"secret-value", "12345.678", "int64", "string into", "dto.", "Go "} {
				assert.NotContains(t, msg, leak)
			}
		})
	}
}

func TestRecurringWritesRequireJSONContentType(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/recurring-plans", validCreate},
		{http.MethodPatch, "/api/v1/recurring-plans/3", `{"quantity":"2"}`},
		{http.MethodPost, "/api/v1/recurring-plans/3/pause", ""},
		{http.MethodPost, "/api/v1/recurring-plans/3/resume", ""},
		{http.MethodPost, "/api/v1/recurring-plans/3/cancel", ""},
	}
	for _, rt := range routes {
		for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx", "bad;;"} {
			t.Run(rt.method+" "+rt.path+" "+ct, func(t *testing.T) {
				// The mock fails the test if any service method is called.
				w := doType(t, mocks.NewMockRecurringService(t), rt.method, rt.path, ct, rt.body)

				require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
				assert.Equal(t, "Content-Type must be application/json", errorBody(t, w))
			})
		}
	}
}

func TestRecurringJSONContentTypeWithCharsetPasses(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Transition(mock.Anything, int64(3), model.PlanCancel).Return(samplePlan(t), nil).Once()

	w := doType(t, svc, http.MethodPost, "/api/v1/recurring-plans/3/cancel", "application/json; charset=utf-8", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRecurringReadsNeedNoContentType(t *testing.T) {
	svc := mocks.NewMockRecurringService(t)
	svc.EXPECT().Get(mock.Anything, int64(3)).Return(samplePlan(t), nil).Once()

	w := doType(t, svc, http.MethodGet, "/api/v1/recurring-plans/3", "", "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
