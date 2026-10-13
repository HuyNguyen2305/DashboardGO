//go:build integration

package dashboard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"dashboardgo/internal/dto"
)

func TestStatsEndpoint(t *testing.T) {
	env := newEnv(t)

	cases := []struct {
		name  string
		query string
		want  dto.StatsResponse
	}{
		{
			name:  "this month matches the screenshot",
			query: "",
			want: dto.StatsResponse{
				Range:               dto.DateRange{Preset: "this_month", Start: "2026-10-01", End: "2026-10-31"},
				Revenue:             "1487.40", // 555.14 + 892.26 + 40.00
				Collected:           "867.26",  // 11 × 65.00 + 152.26
				JobsCompleted:       14,
				NewJobsBookedOnline: "0.00",
				// Estimates 660.22 + 62.00 + 40.00, invoices 555.14 + 892.26; INV-6 skipped (from EST-3).
				TotalNewJobs: "2209.62",
			},
		},
		{
			name:  "september picks up only the september rows",
			query: "?range=last_month",
			want: dto.StatsResponse{
				Range:               dto.DateRange{Preset: "last_month", Start: "2026-09-01", End: "2026-09-30"},
				Revenue:             "500.00",
				Collected:           "99.00",
				JobsCompleted:       0,
				NewJobsBookedOnline: "120.00",
				TotalNewJobs:        "500.00",
			},
		},
		{
			name:  "range with no data returns zeros",
			query: "?from=2025-01-01&to=2025-12-31",
			want: dto.StatsResponse{
				Range:               dto.DateRange{Preset: "custom", Start: "2025-01-01", End: "2025-12-31"},
				Revenue:             "0.00",
				Collected:           "0.00",
				JobsCompleted:       0,
				NewJobsBookedOnline: "0.00",
				TotalNewJobs:        "0.00",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got dto.StatsResponse
			env.getJSON(t, "/api/v1/dashboard/stats"+tc.query, &got)
			assert.Equal(t, tc.want, got)
		})
	}
}
