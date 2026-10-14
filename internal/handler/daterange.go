package handler

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
)

// ParseRange reads the ?range=<preset> picker. Without it, it defaults to This Month.
// Custom from/to ranges are not a product feature, so they are rejected rather than silently ignored.
func ParseRange(c *gin.Context, dates *daterange.Resolver) (daterange.Range, error) {
	q := c.Request.URL.Query()
	if q.Has("from") || q.Has("to") {
		return daterange.Range{}, fmt.Errorf("%w: custom date ranges are not supported, use range=<preset>", ErrInvalidQuery)
	}
	if p := q.Get("range"); p != "" {
		return dates.Preset(daterange.Preset(p))
	}
	return dates.Preset(daterange.ThisMonth)
}

// ParseYear reads ?year= from the request. Without it, or when it is empty, it uses the current year.
// It parses by hand because Gin binds an empty value to 0.
func ParseYear(c *gin.Context, dates *daterange.Resolver) (daterange.Range, error) {
	v := c.Query("year")
	if v == "" {
		return dates.Year(dates.CurrentYear())
	}
	y, err := strconv.Atoi(v)
	if err != nil {
		return daterange.Range{}, fmt.Errorf("%w: year must be a whole number, got %q", daterange.ErrInvalidYear, v)
	}
	return dates.Year(y)
}

// NewDateRange converts a resolved range for responses, with an inclusive end date.
func NewDateRange(r daterange.Range) dto.DateRange {
	const layout = "2006-01-02"
	return dto.DateRange{
		Preset: string(r.Preset),
		Start:  r.Start.Format(layout),
		End:    r.LastDay().Format(layout),
	}
}
