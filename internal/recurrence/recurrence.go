// Package recurrence turns a cadence (every N days, weeks or months from an
// anchor date) into visit dates.
//
// The package is pure: it knows nothing about status, "today" or linked jobs.
// Every visit is computed from the anchor with time.Date on year/month/day,
// never by adding 24h or stepping from the previous visit, so DST cannot move
// a visit and month-end clamping never drifts (Jan 31 gives Feb 28, Mar 31).
package recurrence

import (
	"errors"
	"fmt"
	"time"
)

// Unit is the calendar unit of a cadence step.
type Unit string

// Supported cadence units.
const (
	Day   Unit = "day"
	Week  Unit = "week"
	Month Unit = "month"
)

// ErrInvalidRule is returned (wrapped) by Rule.Validate.
var ErrInvalidRule = errors.New("invalid recurrence rule")

// Rule describes a repeating visit: every Count Units, starting at Start.
type Rule struct {
	Unit  Unit
	Count int        // must be > 0
	Start time.Time  // first visit, local midnight in the business timezone
	End   *time.Time // inclusive; nil means open-ended
}

// Occurrence is one projected visit.
type Occurrence struct {
	Index int       // 0-based visit number counted from Start
	Date  time.Time // local midnight, same Location as Start
}

// Validate reports an unknown unit, a non-positive count, or End before Start.
func (r Rule) Validate() error {
	switch r.Unit {
	case Day, Week, Month:
	default:
		return fmt.Errorf("%w: unknown unit %q", ErrInvalidRule, r.Unit)
	}
	if r.Count <= 0 {
		return fmt.Errorf("%w: count must be greater than 0", ErrInvalidRule)
	}
	if r.End != nil && r.End.Before(r.Start) {
		return fmt.Errorf("%w: end is before start", ErrInvalidRule)
	}
	return nil
}

// At returns the k-th visit (0-based), always computed from the anchor.
// A rule with Count <= 0 is invalid and never advances: every k gives Start.
func (r Rule) At(k int) time.Time {
	if r.Count <= 0 {
		return r.Start
	}
	y, m, d := r.Start.Date()
	loc := r.Start.Location()
	switch r.Unit {
	case Day:
		return time.Date(y, m, d+k*r.Count, 0, 0, 0, 0, loc)
	case Week:
		return time.Date(y, m, d+7*k*r.Count, 0, 0, 0, 0, loc)
	default:
		first := time.Date(y, m+time.Month(k*r.Count), 1, 0, 0, 0, 0, loc)
		last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
		return time.Date(first.Year(), first.Month(), min(d, last), 0, 0, 0, 0, loc)
	}
}

// Between returns the visits in the half-open window [from, to), capped by End
// (inclusive). It never returns nil, and returns no visits for a rule with Count <= 0.
func (r Rule) Between(from, to time.Time) []Occurrence {
	out := []Occurrence{}
	if r.Count <= 0 || !from.Before(to) {
		return out
	}
	for k := r.firstIndex(from); ; k++ {
		date := r.At(k)
		if !date.Before(to) || (r.End != nil && date.After(*r.End)) {
			return out
		}
		if date.Before(from) {
			continue
		}
		out = append(out, Occurrence{Index: k, Date: date})
	}
}

// firstIndex returns an index at or before the first visit on or after from.
func (r Rule) firstIndex(from time.Time) int {
	if !from.After(r.Start) {
		return 0
	}
	if r.Unit == Month {
		months := (from.Year()-r.Start.Year())*12 + int(from.Month()) - int(r.Start.Month())
		// Step back one so day clamping can never skip a visit.
		return max(0, months/r.Count-1)
	}
	step := r.Count
	if r.Unit == Week {
		step *= 7
	}
	days := daysBetween(r.Start, from)
	return max(0, (days+step-1)/step)
}

// daysBetween counts calendar days from a to b using their local dates.
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	ua := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	ub := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua) / (24 * time.Hour))
}

// DateKey formats t as a YYYY-MM-DD calendar date key.
func DateKey(t time.Time) string {
	return t.Format("2006-01-02")
}
