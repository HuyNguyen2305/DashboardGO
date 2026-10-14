package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/middleware"
	"dashboardgo/internal/model"
)

// ErrInvalidQuery marks a malformed query parameter other than the date pickers.
var ErrInvalidQuery = errors.New("invalid query parameter")

// statusClientClosedRequest is nginx's non-standard code for a client that hung up before the response.
// Nobody reads the response; the code just keeps disconnects out of the 5xx (Error) logs.
const statusClientClosedRequest = 499

// badRequestErrors are domain errors caused by client input. Their messages are safe to return.
var badRequestErrors = []error{
	ErrInvalidQuery,
	model.ErrInvalidScope,
	daterange.ErrUnknownPreset,
	daterange.ErrInvalidYear,
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
	for _, target := range badRequestErrors {
		if errors.Is(err, target) {
			return http.StatusBadRequest, err.Error()
		}
	}
	return http.StatusInternalServerError, "internal server error"
}
