package handler

import (
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

// badRequestErrors are domain errors caused by client input. Their messages are safe to return.
var badRequestErrors = []error{
	ErrInvalidQuery,
	model.ErrInvalidScope,
	daterange.ErrUnknownPreset,
	daterange.ErrInvalidDate,
	daterange.ErrRangeOrder,
	daterange.ErrRangeTooLong,
	daterange.ErrInvalidYear,
	daterange.ErrConflictingParams,
}

// WriteError is the one place that maps errors to HTTP responses.
// Client errors get 400 with their message; anything else gets a generic 500,
// with the real error kept on the context for the request logger.
func WriteError(c *gin.Context, err error) {
	_ = c.Error(err)

	status, msg := http.StatusInternalServerError, "internal server error"
	for _, target := range badRequestErrors {
		if errors.Is(err, target) {
			status, msg = http.StatusBadRequest, err.Error()
			break
		}
	}
	c.AbortWithStatusJSON(status, dto.ErrorResponse{Error: msg, RequestID: middleware.GetRequestID(c)})
}
