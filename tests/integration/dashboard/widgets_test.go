//go:build integration

package dashboard_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
)

func row(key string, count int64, amount string, percent float64) dto.BreakdownRow {
	return dto.BreakdownRow{Key: key, Count: count, Amount: amount, Percent: percent}
}

func zeroRows(keys ...string) []dto.BreakdownRow {
	out := make([]dto.BreakdownRow, len(keys))
	for i, k := range keys {
		out[i] = row(k, 0, "0.00", 0)
	}
	return out
}

// dayCounts indexes the daily chart by date for spot checks.
func dayCounts(daily []dto.DailyCounts) map[string]map[string]int64 {
	out := make(map[string]map[string]int64, len(daily))
	for _, d := range daily {
		out[d.Date] = d.Counts
	}
	return out
}

func TestDashboardWidgets(t *testing.T) {
	env := newEnv(t)
	october := dto.DateRange{Preset: "this_month", Start: "2026-10-01", End: "2026-10-31"}

	t.Run("jobs, all schedules", func(t *testing.T) {
		var got dto.JobsResponse
		env.getJSON(t, "/api/v1/dashboard/jobs", &got)

		// Screenshot: $2,360.54; 55% / 42% / 3% by count.
		assert.Equal(t, october, got.Range)
		assert.Equal(t, "2360.54", got.TotalAmount)
		assert.Equal(t, int64(33), got.TotalCount)
		assert.Equal(t, []dto.BreakdownRow{
			row("incomplete", 18, "1371.24", 54.55),
			row("completed", 14, "924.30", 42.42),
			row("reschedule", 0, "0.00", 0),
			row("canceled", 0, "0.00", 0),
			row("terminated", 1, "65.00", 3.03),
			row("workpool", 0, "0.00", 0),
		}, got.Rows)

		require.Len(t, got.Daily, 31)
		days := dayCounts(got.Daily)
		assert.Equal(t, int64(1), days["2026-10-01"]["completed"])
		assert.Equal(t, int64(1), days["2026-10-02"]["completed"])
		assert.Equal(t, int64(1), days["2026-10-02"]["incomplete"])
		// The 00:30 local jobs must land on their local day, not the UTC day before.
		assert.Equal(t, int64(0), days["2026-10-14"]["incomplete"])
		assert.Equal(t, int64(1), days["2026-10-15"]["incomplete"])
		assert.Equal(t, int64(2), days["2026-10-20"]["incomplete"])
		assert.Equal(t, int64(1), days["2026-10-21"]["terminated"])
		assert.Equal(t, map[string]int64{
			"incomplete": 0, "completed": 0, "reschedule": 0, "canceled": 0, "terminated": 0, "workpool": 0,
		}, days["2026-10-31"])
	})

	t.Run("jobs, filtered by schedules", func(t *testing.T) {
		tho, user, sched3 := env.staffID(t, "Thọ"), env.staffID(t, "User"), env.staffID(t, "Schedule 3")
		cases := []struct {
			name        string
			query       string
			totalCount  int64
			totalAmount string
		}{
			{"Thọ only", fmt.Sprintf("?staff_ids=%d", tho), 14, "924.30"},
			{"Thọ and User", fmt.Sprintf("?staff_ids=%d,%d", tho, user), 30, "2099.36"},
			{"schedule with no jobs", fmt.Sprintf("?staff_ids=%d", sched3), 0, "0.00"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var got dto.JobsResponse
				env.getJSON(t, "/api/v1/dashboard/jobs"+tc.query, &got)
				assert.Equal(t, tc.totalCount, got.TotalCount)
				assert.Equal(t, tc.totalAmount, got.TotalAmount)
				assert.Len(t, got.Daily, 31)
			})
		}
	})

	t.Run("invoices", func(t *testing.T) {
		var got dto.BreakdownResponse
		env.getJSON(t, "/api/v1/dashboard/invoices", &got)

		// Screenshot: $2,931.04; 47% / 2% / 19% / 32% by amount.
		assert.Equal(t, dto.BreakdownResponse{
			Range:       october,
			TotalAmount: "2931.04",
			TotalCount:  5,
			Rows: []dto.BreakdownRow{
				row("draft", 1, "1378.64", 47.04),
				row("void", 1, "65.00", 2.22),
				row("sent", 1, "555.14", 18.94),
				row("partial", 0, "0.00", 0),
				row("partial_paid", 0, "0.00", 0),
				row("paid", 2, "932.26", 31.81),
				row("write_off", 0, "0.00", 0),
				row("failed", 0, "0.00", 0),
			},
		}, got)
	})

	t.Run("estimates", func(t *testing.T) {
		var got dto.BreakdownResponse
		env.getJSON(t, "/api/v1/dashboard/estimates", &got)

		assert.Equal(t, dto.BreakdownResponse{
			Range:       october,
			TotalAmount: "1062.22",
			TotalCount:  4,
			Rows: []dto.BreakdownRow{
				row("draft", 1, "660.22", 62.15), // 660.22 / 1062.22 = 62.1547%
				row("pending", 1, "62.00", 5.84),
				row("won", 0, "0.00", 0),
				row("invoiced", 1, "40.00", 3.77),
				row("lost", 1, "300.00", 28.24),
			},
		}, got)
	})

	t.Run("invoices aging as of today", func(t *testing.T) {
		var got dto.AgingResponse
		env.getJSON(t, "/api/v1/dashboard/invoices/aging", &got)

		assert.Equal(t, dto.AgingResponse{
			AsOf:        "2026-10-12",
			TotalAmount: "972.91",
			TotalCount:  6,
			Rows: []dto.BreakdownRow{
				row("0_30", 3, "657.91", 67.62),    // INV-1 555.14 (no due date) + INV-A1 92.77 (not due) + INV-A6 10.00 (30 days)
				row("31_60", 1, "20.00", 2.06),     // INV-A7, 31 days
				row("61_90", 1, "95.00", 9.76),     // INV-A2, 84 days
				row("90_plus", 1, "200.00", 20.56), // INV-A3 balance after a 100.00 payment
			},
		}, got)
	})

	t.Run("revenue per month", func(t *testing.T) {
		var got dto.YearSeriesResponse
		env.getJSON(t, "/api/v1/dashboard/revenue-per-month?year=2026", &got)

		assert.Equal(t, dto.DateRange{Preset: "year", Start: "2026-01-01", End: "2026-12-31"}, got.Range)
		assert.Equal(t, "2687.40", got.Total)
		assert.Equal(t, []dto.MonthlyAmount{
			{Month: "2026-01", Amount: "0.00"},
			{Month: "2026-02", Amount: "0.00"},
			{Month: "2026-03", Amount: "0.00"},
			{Month: "2026-04", Amount: "0.00"},
			{Month: "2026-05", Amount: "0.00"},
			{Month: "2026-06", Amount: "300.00"},
			{Month: "2026-07", Amount: "95.00"}, // INV-A2 issued Jul 1 00:30 local (Jun 30 in UTC)
			{Month: "2026-08", Amount: "305.00"},
			{Month: "2026-09", Amount: "500.00"},
			{Month: "2026-10", Amount: "1487.40"},
			{Month: "2026-11", Amount: "0.00"},
			{Month: "2026-12", Amount: "0.00"},
		}, got.Months)
	})

	t.Run("sales forecast", func(t *testing.T) {
		var got dto.YearSeriesResponse
		env.getJSON(t, "/api/v1/dashboard/sales-forecast", &got) // defaults to the current year

		assert.Equal(t, dto.DateRange{Preset: "year", Start: "2026-01-01", End: "2026-12-31"}, got.Range)
		assert.Equal(t, "3559.62", got.Total)
		assert.Equal(t, "2209.62", got.Months[9].Amount, "October must equal Total New Jobs in the Stats row")
		assert.Equal(t, "150.00", got.Months[10].Amount)
		assert.Equal(t, "305.00", got.Months[7].Amount)
	})

	t.Run("payments", func(t *testing.T) {
		cases := []struct {
			name  string
			query string
			want  dto.BreakdownResponse
		}{
			{"october, biggest first", "", dto.BreakdownResponse{
				Range: october, TotalAmount: "867.26", TotalCount: 12,
				Rows: []dto.BreakdownRow{row("check", 11, "715.00", 82.44), row("stripe", 1, "152.26", 17.56)},
			}},
			{"september", "?range=last_month", dto.BreakdownResponse{
				Range:       dto.DateRange{Preset: "last_month", Start: "2026-09-01", End: "2026-09-30"},
				TotalAmount: "99.00", TotalCount: 1,
				Rows: []dto.BreakdownRow{row("check", 1, "99.00", 100)},
			}},
			{"no payments", "?from=2025-01-01&to=2025-01-31", dto.BreakdownResponse{
				Range:       dto.DateRange{Preset: "custom", Start: "2025-01-01", End: "2025-01-31"},
				TotalAmount: "0.00", TotalCount: 0, Rows: []dto.BreakdownRow{},
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var got dto.BreakdownResponse
				env.getJSON(t, "/api/v1/dashboard/payments"+tc.query, &got)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("secondary widgets", func(t *testing.T) { testSecondaryWidgets(t, env) })

	t.Run("empty range keeps every status row", func(t *testing.T) {
		var got dto.BreakdownResponse
		env.getJSON(t, "/api/v1/dashboard/estimates?from=2025-01-01&to=2025-01-31", &got)
		assert.Equal(t, zeroRows("draft", "pending", "won", "invoiced", "lost"), got.Rows)
		assert.Equal(t, "0.00", got.TotalAmount)
	})
}
