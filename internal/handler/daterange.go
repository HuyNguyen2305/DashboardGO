package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
)

// ParseRange reads ?range= or ?from=&to= from the request. With neither, it defaults to This Month.
func ParseRange(c *gin.Context, dates *daterange.Resolver) (daterange.Range, error) {
	var q dto.RangeQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		return daterange.Range{}, fmt.Errorf("%w: %w", daterange.ErrInvalidDate, err)
	}

	hasCustom := q.From != "" || q.To != ""
	switch {
	case q.Range != "" && hasCustom:
		return daterange.Range{}, fmt.Errorf("%w: use either range or from/to, not both", daterange.ErrConflictingParams)
	case hasCustom:
		return dates.Custom(q.From, q.To)
	case q.Range != "":
		return dates.Preset(daterange.Preset(q.Range))
	default:
		return dates.Preset(daterange.ThisMonth)
	}
}

// ParseYear reads ?year= from the request. Without it, it uses the current year.
func ParseYear(c *gin.Context, dates *daterange.Resolver) (daterange.Range, error) {
	var q dto.YearQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		return daterange.Range{}, fmt.Errorf("%w: year must be a whole number", daterange.ErrInvalidYear)
	}
	if q.Year == nil {
		return dates.Year(dates.CurrentYear())
	}
	return dates.Year(*q.Year)
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
