package daterange_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

// resolverAt returns a Resolver in loc whose clock is frozen at the given local time.
func resolverAt(loc *time.Location, y int, m time.Month, d, hh, mm int) *daterange.Resolver {
	now := time.Date(y, m, d, hh, mm, 0, 0, loc)
	return daterange.NewResolver(loc, func() time.Time { return now })
}

// assertDays checks the range covers firstDay..lastDay inclusive, starting and ending at local midnight.
func assertDays(t *testing.T, r daterange.Range, loc *time.Location, firstDay, lastDay string) {
	t.Helper()
	const layout = "2006-01-02"
	assert.Equal(t, firstDay, r.Start.Format(layout), "first day")
	assert.Equal(t, lastDay, r.LastDay().Format(layout), "last day")
	for _, ts := range []time.Time{r.Start, r.End} {
		local := ts.In(loc)
		assert.Equal(t, 0, local.Hour()*3600+local.Minute()*60+local.Second()+local.Nanosecond(), "boundary %s must be local midnight", local)
	}
}

func TestPresetsMatchDatePicker(t *testing.T) {
	// The date-picker screenshot was taken on Mon 2026-10-12.
	loc := mustLoad(t, "Asia/Ho_Chi_Minh")
	res := resolverAt(loc, 2026, time.October, 12, 10, 0)

	cases := []struct {
		preset          daterange.Preset
		first, lastIncl string
	}{
		{daterange.Today, "2026-10-12", "2026-10-12"},
		{daterange.ThisWeek, "2026-10-11", "2026-10-17"},
		{daterange.Last7Days, "2026-10-06", "2026-10-12"},
		{daterange.ThisMonth, "2026-10-01", "2026-10-31"},
		{daterange.Last30Days, "2026-09-13", "2026-10-12"},
		{daterange.LastMonth, "2026-09-01", "2026-09-30"},
		{daterange.Last3Months, "2026-07-01", "2026-09-30"},
		{daterange.Last6Months, "2026-04-01", "2026-09-30"},
		{daterange.ThisYear, "2026-01-01", "2026-12-31"},
	}
	for _, tc := range cases {
		t.Run(string(tc.preset), func(t *testing.T) {
			r, err := res.Preset(tc.preset)
			require.NoError(t, err)
			assert.Equal(t, tc.preset, r.Preset)
			assertDays(t, r, loc, tc.first, tc.lastIncl)
		})
	}
}

func TestPresetEdgeCases(t *testing.T) {
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")

	cases := []struct {
		name            string
		res             *daterange.Resolver
		preset          daterange.Preset
		first, lastIncl string
	}{
		// 00:30 on Oct 12 in GMT+7 is still Oct 11 in UTC; "today" must follow the business timezone.
		{"today just after local midnight", resolverAt(hcm, 2026, time.October, 12, 0, 30), daterange.Today, "2026-10-12", "2026-10-12"},
		{"today just before local midnight", resolverAt(hcm, 2026, time.October, 12, 23, 59), daterange.Today, "2026-10-12", "2026-10-12"},
		{"last month across new year", resolverAt(hcm, 2027, time.January, 15, 9, 0), daterange.LastMonth, "2026-12-01", "2026-12-31"},
		{"last 3 months across new year", resolverAt(hcm, 2027, time.January, 15, 9, 0), daterange.Last3Months, "2026-10-01", "2026-12-31"},
		{"last 6 months across new year", resolverAt(hcm, 2027, time.January, 15, 9, 0), daterange.Last6Months, "2026-07-01", "2026-12-31"},
		{"last 30 days across new year", resolverAt(hcm, 2027, time.January, 15, 9, 0), daterange.Last30Days, "2026-12-17", "2027-01-15"},
		{"this week when today is Sunday", resolverAt(hcm, 2026, time.October, 11, 9, 0), daterange.ThisWeek, "2026-10-11", "2026-10-17"},
		{"this week when today is Saturday", resolverAt(hcm, 2026, time.October, 17, 9, 0), daterange.ThisWeek, "2026-10-11", "2026-10-17"},
		{"this week spanning two months", resolverAt(hcm, 2026, time.November, 2, 9, 0), daterange.ThisWeek, "2026-11-01", "2026-11-07"},
		{"this month in leap February", resolverAt(hcm, 2028, time.February, 10, 9, 0), daterange.ThisMonth, "2028-02-01", "2028-02-29"},
		{"last month from March 31", resolverAt(hcm, 2026, time.March, 31, 9, 0), daterange.LastMonth, "2026-02-01", "2026-02-28"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.res.Preset(tc.preset)
			require.NoError(t, err)
			assertDays(t, r, hcm, tc.first, tc.lastIncl)
		})
	}
}

func TestPresetAcrossDaylightSaving(t *testing.T) {
	// Clocks in New York jump forward on 2026-03-08, so that day is only 23 hours long.
	ny := mustLoad(t, "America/New_York")
	res := resolverAt(ny, 2026, time.March, 8, 12, 0)

	r, err := res.Preset(daterange.Today)
	require.NoError(t, err)
	assertDays(t, r, ny, "2026-03-08", "2026-03-08")
	assert.Equal(t, 23*time.Hour, r.End.Sub(r.Start))

	week, err := res.Preset(daterange.ThisWeek)
	require.NoError(t, err)
	assertDays(t, week, ny, "2026-03-08", "2026-03-14")
}

func TestUnknownPreset(t *testing.T) {
	res := resolverAt(time.UTC, 2026, time.October, 12, 0, 0)
	for _, p := range []daterange.Preset{"", "yesterday", "custom", "year", "THIS_MONTH"} {
		_, err := res.Preset(p)
		assert.ErrorIs(t, err, daterange.ErrUnknownPreset, "preset %q", p)
	}
}

func TestYear(t *testing.T) {
	loc := mustLoad(t, "Asia/Ho_Chi_Minh")
	res := resolverAt(loc, 2026, time.October, 12, 10, 0)

	r, err := res.Year(2026)
	require.NoError(t, err)
	assert.Equal(t, daterange.Year, r.Preset)
	assertDays(t, r, loc, "2026-01-01", "2026-12-31")

	for _, y := range []int{0, 1999, 2101} {
		_, err := res.Year(y)
		assert.ErrorIs(t, err, daterange.ErrInvalidYear, "year %d", y)
	}
	for _, y := range []int{daterange.MinYear, daterange.MaxYear} {
		_, err := res.Year(y)
		assert.NoError(t, err, "year %d", y)
	}
}

func TestCurrentYearUsesBusinessTimezone(t *testing.T) {
	// 2026-12-31 18:00 UTC is already 2027-01-01 01:00 in GMT+7.
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")
	now := time.Date(2026, time.December, 31, 18, 0, 0, 0, time.UTC)
	res := daterange.NewResolver(hcm, func() time.Time { return now })

	assert.Equal(t, 2027, res.CurrentYear())
	r, err := res.Preset(daterange.Today)
	require.NoError(t, err)
	assertDays(t, r, hcm, "2027-01-01", "2027-01-01")
}

func TestCapAt(t *testing.T) {
	loc := mustLoad(t, "Asia/Ho_Chi_Minh")
	month, err := resolverAt(loc, 2026, time.October, 12, 10, 0).Preset(daterange.ThisMonth)
	require.NoError(t, err)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	cases := []struct {
		name    string
		t       time.Time
		wantEnd time.Time
	}{
		{"inside the range moves End to t", at(time.October, 12, 10), at(time.October, 12, 10)},
		{"after the range keeps End", at(time.November, 5, 0), month.End},
		{"exactly at End keeps End", month.End, month.End},
		{"before the range leaves it empty", at(time.September, 1, 0), month.Start},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := month.CapAt(tc.t)
			assert.True(t, got.End.Equal(tc.wantEnd), "End %s, want %s", got.End, tc.wantEnd)
			assert.Equal(t, month.Start, got.Start)
			assert.Equal(t, month.Preset, got.Preset)
		})
	}
}

func TestResolverNow(t *testing.T) {
	hcm := mustLoad(t, "Asia/Ho_Chi_Minh")
	utc := time.Date(2026, time.October, 11, 18, 30, 0, 0, time.UTC)
	got := daterange.NewResolver(hcm, func() time.Time { return utc }).Now()
	assert.True(t, got.Equal(utc))
	assert.Equal(t, hcm, got.Location())
}
