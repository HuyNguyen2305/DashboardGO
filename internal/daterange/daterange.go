// Package daterange turns dashboard date pickers (fixed presets and years)
// into half-open [Start, End) ranges in the business timezone.
//
// Every widget must resolve its range here so their numbers agree. Queries filter
// with `col >= Start AND col < End`; End is the midnight after the last day.
package daterange

import (
	"errors"
	"fmt"
	"time"
)

// Preset names a quick-pick option in the date picker.
type Preset string

// Presets accepted in the `range` query parameter, plus the kind produced by Year.
// Users only pick fixed presets; there are deliberately no custom from/to ranges.
const (
	Today       Preset = "today"
	ThisWeek    Preset = "this_week"
	Last7Days   Preset = "last_7_days"
	ThisMonth   Preset = "this_month"
	Last30Days  Preset = "last_30_days"
	LastMonth   Preset = "last_month"
	Last3Months Preset = "last_3_months"
	Last6Months Preset = "last_6_months"
	ThisYear    Preset = "this_year"
	Year        Preset = "year"
)

// Limits for the year picker.
const (
	MinYear = 2000
	MaxYear = 2100
)

// Errors returned for invalid input. Handlers map all of them to 400.
var (
	ErrUnknownPreset = errors.New("unknown date range")
	ErrInvalidYear   = errors.New("invalid year")
)

// Range is a half-open interval [Start, End) of whole days in the business timezone.
type Range struct {
	Preset Preset
	Start  time.Time
	End    time.Time
}

// CapAt returns the range with End moved back to t when t falls before it, so rows dated
// after t (for example future-dated payments in "This Month") are left out. Start is kept,
// and End never moves before Start. The result is for querying; responses still echo r.
func (r Range) CapAt(t time.Time) Range {
	switch {
	case t.Before(r.Start):
		r.End = r.Start
	case t.Before(r.End):
		r.End = t
	}
	return r
}

// LastDay returns the last day included in the range (End minus one calendar day).
func (r Range) LastDay() time.Time {
	return r.End.AddDate(0, 0, -1)
}

// Days returns local midnight of every day in the range, in order.
func (r Range) Days() []time.Time {
	var days []time.Time
	for d := r.Start; d.Before(r.End); {
		days = append(days, d)
		y, m, dd := d.Date()
		d = time.Date(y, m, dd+1, 0, 0, 0, 0, d.Location())
	}
	return days
}

// Months returns the first day (local midnight) of every month the range touches, in order.
func (r Range) Months() []time.Time {
	var months []time.Time
	y, m, _ := r.Start.Date()
	for d := time.Date(y, m, 1, 0, 0, 0, 0, r.Start.Location()); d.Before(r.End); {
		months = append(months, d)
		d = time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, d.Location())
	}
	return months
}

// Resolver computes ranges relative to "now" in a fixed timezone.
type Resolver struct {
	loc *time.Location
	now func() time.Time
}

// NewResolver returns a Resolver for loc. Pass time.Now in production and a fixed clock in tests.
func NewResolver(loc *time.Location, now func() time.Time) *Resolver {
	return &Resolver{loc: loc, now: now}
}

// Now returns the current instant in the business timezone.
func (r *Resolver) Now() time.Time {
	return r.now().In(r.loc)
}

// CurrentYear returns the current year in the business timezone.
func (r *Resolver) CurrentYear() int {
	return r.now().In(r.loc).Year()
}

// Today returns local midnight of the current day in the business timezone.
func (r *Resolver) Today() time.Time {
	y, m, d := r.now().In(r.loc).Date()
	return r.date(y, m, d)
}

// Preset resolves a quick-pick option. Calendar math always goes through time.Date,
// so day lengths stay correct across daylight-saving changes.
func (r *Resolver) Preset(p Preset) (Range, error) {
	y, m, d := r.now().In(r.loc).Date()

	var start, end time.Time
	switch p {
	case Today:
		start, end = r.date(y, m, d), r.date(y, m, d+1)
	case ThisWeek:
		// Weeks run Sunday to Saturday.
		sunday := d - int(r.date(y, m, d).Weekday())
		start, end = r.date(y, m, sunday), r.date(y, m, sunday+7)
	case Last7Days:
		start, end = r.date(y, m, d-6), r.date(y, m, d+1)
	case Last30Days:
		start, end = r.date(y, m, d-29), r.date(y, m, d+1)
	case ThisMonth:
		start, end = r.date(y, m, 1), r.date(y, m+1, 1)
	case LastMonth:
		start, end = r.date(y, m-1, 1), r.date(y, m, 1)
	case Last3Months:
		start, end = r.date(y, m-3, 1), r.date(y, m, 1)
	case Last6Months:
		start, end = r.date(y, m-6, 1), r.date(y, m, 1)
	case ThisYear:
		start, end = r.date(y, 1, 1), r.date(y+1, 1, 1)
	default:
		return Range{}, fmt.Errorf("%w: %q", ErrUnknownPreset, string(p))
	}
	return Range{Preset: p, Start: start, End: end}, nil
}

// Year resolves Jan 1 – Dec 31 of year y, for the yearly charts.
func (r *Resolver) Year(y int) (Range, error) {
	if y < MinYear || y > MaxYear {
		return Range{}, fmt.Errorf("%w: %d, must be between %d and %d", ErrInvalidYear, y, MinYear, MaxYear)
	}
	return Range{Preset: Year, Start: r.date(y, 1, 1), End: r.date(y+1, 1, 1)}, nil
}

// date returns local midnight; time.Date normalizes overflow such as day 0 or month 13.
func (r *Resolver) date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, r.loc)
}
