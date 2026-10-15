package recurrence_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/recurrence"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func ptr(t time.Time) *time.Time { return &t }

func TestAt(t *testing.T) {
	tests := []struct {
		name string
		rule recurrence.Rule
		k    int
		want time.Time
	}{
		{"day step 3, k=2", recurrence.Rule{Unit: recurrence.Day, Count: 3, Start: d(2026, 1, 1)}, 2, d(2026, 1, 7)},
		{"week step 2, k=1", recurrence.Rule{Unit: recurrence.Week, Count: 2, Start: d(2026, 1, 5)}, 1, d(2026, 1, 19)},
		{"month clamp Feb", recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2026, 1, 31)}, 1, d(2026, 2, 28)},
		{"month no drift Mar", recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2026, 1, 31)}, 2, d(2026, 3, 31)},
		{"month clamp Apr", recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2026, 1, 31)}, 3, d(2026, 4, 30)},
		{"month leap Feb", recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2028, 1, 31)}, 1, d(2028, 2, 29)},
		{"yearly from leap day, non-leap", recurrence.Rule{Unit: recurrence.Month, Count: 12, Start: d(2028, 2, 29)}, 1, d(2029, 2, 28)},
		{"yearly from leap day, back to leap", recurrence.Rule{Unit: recurrence.Month, Count: 12, Start: d(2028, 2, 29)}, 4, d(2032, 2, 29)},
		{"k=0 is Start", recurrence.Rule{Unit: recurrence.Month, Count: 5, Start: d(2026, 1, 31)}, 0, d(2026, 1, 31)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.want.Equal(tt.rule.At(tt.k)), "got %s want %s", tt.rule.At(tt.k), tt.want)
		})
	}
}

type betweenCase struct {
	name     string
	rule     recurrence.Rule
	from, to time.Time
	want     []recurrence.Occurrence
}

func betweenCases() []betweenCase {
	daily := recurrence.Rule{Unit: recurrence.Day, Count: 1, Start: d(2026, 1, 10), End: ptr(d(2026, 1, 15))}
	return []betweenCase{
		{"window before Start", daily, d(2026, 1, 1), d(2026, 1, 10), []recurrence.Occurrence{}},
		{"window after End", daily, d(2026, 1, 16), d(2026, 2, 1), []recurrence.Occurrence{}},
		{"visit exactly on End included", daily, d(2026, 1, 14), d(2026, 2, 1), []recurrence.Occurrence{
			{Index: 4, Date: d(2026, 1, 14)}, {Index: 5, Date: d(2026, 1, 15)},
		}},
		{"visit exactly on to excluded", daily, d(2026, 1, 10), d(2026, 1, 12), []recurrence.Occurrence{
			{Index: 0, Date: d(2026, 1, 10)}, {Index: 1, Date: d(2026, 1, 11)},
		}},
		{"from exactly on a visit included", daily, d(2026, 1, 12), d(2026, 1, 14), []recurrence.Occurrence{
			{Index: 2, Date: d(2026, 1, 12)}, {Index: 3, Date: d(2026, 1, 13)},
		}},
		{"from equals to", daily, d(2026, 1, 12), d(2026, 1, 12), []recurrence.Occurrence{}},
		{"from after to", daily, d(2026, 1, 14), d(2026, 1, 12), []recurrence.Occurrence{}},
		{
			"month-end clamp far from Start",
			recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2025, 1, 31)},
			d(2026, 10, 15), d(2026, 12, 1),
			[]recurrence.Occurrence{{Index: 21, Date: d(2026, 10, 31)}, {Index: 22, Date: d(2026, 11, 30)}},
		},
		{
			"weekly open-ended far from Start",
			recurrence.Rule{Unit: recurrence.Week, Count: 2, Start: d(2026, 1, 5)},
			d(2026, 3, 1), d(2026, 3, 20),
			[]recurrence.Occurrence{{Index: 4, Date: d(2026, 3, 2)}, {Index: 5, Date: d(2026, 3, 16)}},
		},
	}
}

func TestBetween(t *testing.T) {
	for _, tt := range betweenCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Between(tt.from, tt.to)
			require.NotNil(t, got)
			require.Len(t, got, len(tt.want))
			for i := range tt.want {
				assert.Equal(t, tt.want[i].Index, got[i].Index)
				assert.True(t, tt.want[i].Date.Equal(got[i].Date), "got %s want %s", got[i].Date, tt.want[i].Date)
			}
		})
	}
}

func TestBetweenProperties(t *testing.T) {
	for _, tt := range betweenCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Between(tt.from, tt.to)
			for i, o := range got {
				assert.False(t, o.Date.Before(tt.from), "before from")
				assert.True(t, o.Date.Before(tt.to), "not before to")
				if tt.rule.End != nil {
					assert.False(t, o.Date.After(*tt.rule.End), "after End")
				}
				assert.True(t, o.Date.Equal(tt.rule.At(o.Index)), "date != At(index)")
				if i > 0 {
					assert.True(t, o.Date.After(got[i-1].Date), "not strictly increasing")
				}
			}
		})
	}
}

func TestBetweenDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	tests := []struct {
		name     string
		start    time.Time
		from, to time.Time
	}{
		{"spring forward", time.Date(2026, 3, 7, 0, 0, 0, 0, loc), time.Date(2026, 3, 7, 0, 0, 0, 0, loc), time.Date(2026, 3, 10, 0, 0, 0, 0, loc)},
		{"fall back", time.Date(2026, 10, 31, 0, 0, 0, 0, loc), time.Date(2026, 10, 31, 0, 0, 0, 0, loc), time.Date(2026, 11, 3, 0, 0, 0, 0, loc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := recurrence.Rule{Unit: recurrence.Day, Count: 1, Start: tt.start}
			got := r.Between(tt.from, tt.to)
			require.Len(t, got, 3)
			for _, o := range got {
				assert.Equal(t, 0, o.Date.Hour(), "visit %s must be local midnight", o.Date)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		rule    recurrence.Rule
		wantErr bool
	}{
		{"valid", recurrence.Rule{Unit: recurrence.Month, Count: 1, Start: d(2026, 1, 1)}, false},
		{"valid with End equal Start", recurrence.Rule{Unit: recurrence.Day, Count: 1, Start: d(2026, 1, 1), End: ptr(d(2026, 1, 1))}, false},
		{"unknown unit", recurrence.Rule{Unit: "year", Count: 1, Start: d(2026, 1, 1)}, true},
		{"zero count", recurrence.Rule{Unit: recurrence.Day, Count: 0, Start: d(2026, 1, 1)}, true},
		{"End before Start", recurrence.Rule{Unit: recurrence.Day, Count: 1, Start: d(2026, 1, 2), End: ptr(d(2026, 1, 1))}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, recurrence.ErrInvalidRule))
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestDateKey(t *testing.T) {
	assert.Equal(t, "2026-03-07", recurrence.DateKey(d(2026, 3, 7)))
}

func TestNonPositiveCountIsGuarded(t *testing.T) {
	for _, unit := range []recurrence.Unit{recurrence.Day, recurrence.Week, recurrence.Month} {
		for _, count := range []int{0, -1, -12} {
			rule := recurrence.Rule{Unit: unit, Count: count, Start: d(2026, 1, 15)}
			t.Run(fmt.Sprintf("%s %d", unit, count), func(t *testing.T) {
				// Without the guard Between loops forever (count 0) or divides by zero.
				done := make(chan []recurrence.Occurrence, 1)
				go func() { done <- rule.Between(d(2026, 1, 1), d(2027, 1, 1)) }()
				select {
				case got := <-done:
					require.NotNil(t, got)
					assert.Empty(t, got)
				case <-time.After(2 * time.Second):
					t.Fatal("Between did not return")
				}
				for _, k := range []int{0, 1, 5} {
					assert.True(t, rule.At(k).Equal(rule.Start), "At(%d) of an invalid rule is Start", k)
				}
			})
		}
	}
}
