package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/middleware"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

// ErrInvalidQuery marks a malformed query parameter other than the date pickers.
var ErrInvalidQuery = errors.New("invalid query parameter")

// ErrInvalidBody marks a malformed or invalid JSON request body.
var ErrInvalidBody = errors.New("invalid request body")

// ErrBodyTooLarge marks a request body over the size limit.
var ErrBodyTooLarge = errors.New("request body too large")

// ErrUnsupportedMediaType marks a write request whose Content-Type is not JSON.
var ErrUnsupportedMediaType = errors.New("Content-Type must be application/json")

// statusClientClosedRequest is nginx's non-standard code for a client that hung up before the response.
// Nobody reads the response; the code just keeps disconnects out of the 5xx (Error) logs.
const statusClientClosedRequest = 499

// badRequestErrors are domain errors caused by client input. Their messages are safe to return.
var badRequestErrors = []error{
	ErrInvalidQuery,
	model.ErrInvalidScope,
	daterange.ErrUnknownPreset,
	daterange.ErrInvalidYear,
	ErrInvalidBody,
	model.ErrInvalidReference,
	model.ErrInvalidPlan,
	model.ErrWindowTooLarge,
	recurrence.ErrInvalidRule,
}

// notFoundErrors are domain errors for a missing resource.
var notFoundErrors = []error{
	model.ErrPlanNotFound,
	model.ErrCustomerNotFound,
}

// conflictErrors are domain errors for a request that clashes with the current state.
var conflictErrors = []error{
	model.ErrPlanCanceled,
	model.ErrInvalidTransition,
	model.ErrCadenceLocked,
}

// WriteError is the one place that maps errors to HTTP responses.
// Client errors get 400 with their message, a query timeout gets 504, a client that hung up
// gets 499, and anything else gets a generic 500. The real error is kept on the context for the request logger.
func WriteError(c *gin.Context, err error) {
	_ = c.Error(err)
	status, msg := errorStatus(err)
	c.AbortWithStatusJSON(status, dto.ErrorResponse{Error: msg, RequestID: middleware.GetRequestID(c)})
}

func errorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "request timed out"
	case errors.Is(err, context.Canceled):
		return statusClientClosedRequest, "request canceled"
	}
	for _, group := range clientErrors {
		for _, target := range group.targets {
			if errors.Is(err, target) {
				return group.status, clientMessage(err, target)
			}
		}
	}
	return http.StatusInternalServerError, "internal server error"
}

// clientErrors lists each client-error group with its status, checked in order.
var clientErrors = []struct {
	status  int
	targets []error
}{
	{http.StatusRequestEntityTooLarge, []error{ErrBodyTooLarge}},
	{http.StatusUnsupportedMediaType, []error{ErrUnsupportedMediaType}},
	{http.StatusBadRequest, badRequestErrors},
	{http.StatusNotFound, notFoundErrors},
	{http.StatusConflict, conflictErrors},
}

// clientMessage is the part of err's message a client may see: it starts at the sentinel's
// own text, dropping the wrap prefixes before it, and stops at the first newline, dropping
// anything errors.Join added (such as a rollback failure).
func clientMessage(err, sentinel error) string {
	msg, want := err.Error(), sentinel.Error()
	i := strings.Index(msg, want)
	if i < 0 {
		return want
	}
	msg, _, _ = strings.Cut(msg[i:], "\n")
	return msg
}
