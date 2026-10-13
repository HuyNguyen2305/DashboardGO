package daterange_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
)

func formatAll(ts []time.Time, layout string) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format(layout)
	}
	return out
}

func TestDays(t *testing.T) {
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")
	res := resolverAt(hcm, 2026, time.October, 12, 10, 0)

	month, err := res.Preset(daterange.ThisMonth)
	require.NoError(t, err)
	days := month.Days()
	require.Len(t, days, 31)
	assert.Equal(t, "2026-10-01", days[0].Format("2006-01-02"))
	assert.Equal(t, "2026-10-31", days[30].Format("2006-01-02"))
	for _, d := range days {
		assert.Equal(t, hcm, d.Location())
		assert.Zero(t, d.Hour(), "%s must be local midnight", d)
	}

	today, err := res.Preset(daterange.Today)
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-10-12"}, formatAll(today.Days(), "2006-01-02"))
}

func TestDaysAcrossDaylightSaving(t *testing.T) {
	// 2026-03-08 is 23 hours long in New York; it must still be exactly one day.
	ny := mustLoad(t, "America/New_York")
	r, err := resolverAt(ny, 2026, time.March, 10, 12, 0).Custom("2026-03-07", "2026-03-09")
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-03-07", "2026-03-08", "2026-03-09"}, formatAll(r.Days(), "2006-01-02"))
}

func TestMonths(t *testing.T) {
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")
	res := resolverAt(hcm, 2026, time.October, 12, 10, 0)

	year, err := res.Year(2026)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06",
		"2026-07", "2026-08", "2026-09", "2026-10", "2026-11", "2026-12",
	}, formatAll(year.Months(), "2006-01"))
	for _, m := range year.Months() {
		assert.Equal(t, 1, m.Day())
		assert.Zero(t, m.Hour())
	}

	acrossNewYear, err := res.Custom("2026-11-15", "2027-02-03")
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-11", "2026-12", "2027-01", "2027-02"}, formatAll(acrossNewYear.Months(), "2006-01"))

	oneDay, err := res.Preset(daterange.Today)
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-10"}, formatAll(oneDay.Months(), "2006-01"))
}

func TestToday(t *testing.T) {
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"morning", time.Date(2026, time.October, 12, 10, 0, 0, 0, hcm), "2026-10-12"},
		{"just after local midnight, still yesterday in UTC", time.Date(2026, time.October, 12, 0, 5, 0, 0, hcm), "2026-10-12"},
		{"clock reports UTC", time.Date(2026, time.October, 11, 18, 0, 0, 0, time.UTC), "2026-10-12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := daterange.NewResolver(hcm, func() time.Time { return tc.now }).Today()
			assert.Equal(t, tc.want, got.Format("2006-01-02"))
			assert.Equal(t, hcm, got.Location())
			assert.Zero(t, got.Hour())
		})
	}
}
