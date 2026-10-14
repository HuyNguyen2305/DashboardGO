//go:build integration

package dashboard_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"

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

// assertSharesSum100 checks that a widget's percents add up to exactly 100 (or are all 0).
func assertSharesSum100(t *testing.T, rows []dto.BreakdownRow) {
	t.Helper()
	sum := decimal.Zero
	for _, r := range rows {
		sum = sum.Add(decimal.NewFromFloat(r.Percent))
	}
	assert.True(t, sum.Equal(decimal.NewFromInt(100)), "percents sum to %s", sum)
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
		assertSharesSum100(t, got.Rows)

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
			// October jobs with no staff: 76.18 incomplete, 65.00 terminated, 120.00 incomplete.
			{"unassigned only", "?staff_ids=unassigned", 3, "261.18"},
			{"Thọ and unassigned", fmt.Sprintf("?staff_ids=%d,unassigned", tho), 17, "1185.48"},
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
		assertSharesSum100(t, got.Rows)

		// Screenshot: $2,931.04; 47% / 2% / 19% / 32% by amount.
		assert.Equal(t, dto.BreakdownResponse{
			Range:       october,
			TotalAmount: "2931.04",
			TotalCount:  5,
			Rows: []dto.BreakdownRow{
				row("draft", 1, "1378.64", 47.03), // largest remainder: on their own the shares sum to 100.01
				row("void", 1, "65.00", 2.22),
				row("sent", 1, "555.14", 18.94),
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
		assertSharesSum100(t, got.Rows)

		assert.Equal(t, dto.AgingResponse{
			AsOf:        "2026-10-12",
			TotalAmount: "972.91",
			TotalCount:  6,
			Rows: []dto.BreakdownRow{
				row("current", 1, "92.77", 9.53),   // INV-A1, due Oct 20: not due yet (9.535%, gives up the spare 0.01)
				row("1_30", 2, "565.14", 58.09),    // INV-1 555.14 (no due date, 9 days) + INV-A6 10.00 (30 days)
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
		// Booked jobs per scheduled month: October is every October job but the terminated 65.00
		// (the Jobs widget's 2360.54 - 65.00); November is the completed 50.00 job.
		assert.Equal(t, "2345.54", got.Total)
		for i, m := range got.Months {
			want := "0.00"
			switch m.Month {
			case "2026-10":
				want = "2295.54"
			case "2026-11":
				want = "50.00"
			}
			assert.Equal(t, want, got.Months[i].Amount, m.Month)
		}
	})

	t.Run("payments", func(t *testing.T) {
		cases := []struct {
			name       string
			query      string
			before2026 bool // serve with the clock in February 2025
			want       dto.BreakdownResponse
		}{
			{"october, biggest first", "", false, dto.BreakdownResponse{
				Range: october, TotalAmount: "867.26", TotalCount: 12,
				Rows: append([]dto.BreakdownRow{row("check", 11, "715.00", 82.44), row("stripe", 1, "152.26", 17.56)},
					zeroRows("card", "cash", "other")...),
			}},
			{"september", "?range=last_month", false, dto.BreakdownResponse{
				Range:       dto.DateRange{Preset: "last_month", Start: "2026-09-01", End: "2026-09-30"},
				TotalAmount: "99.00", TotalCount: 1,
				Rows: append([]dto.BreakdownRow{row("check", 1, "99.00", 100)}, zeroRows("card", "cash", "other", "stripe")...),
			}},
			{"no payments", "?range=last_month", true, dto.BreakdownResponse{
				Range:       dto.DateRange{Preset: "last_month", Start: "2025-01-01", End: "2025-01-31"},
				TotalAmount: "0.00", TotalCount: 0, Rows: zeroRows("card", "cash", "check", "other", "stripe"),
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := env
				if tc.before2026 {
					e = env.before2026(t)
				}
				var got dto.BreakdownResponse
				e.getJSON(t, "/api/v1/dashboard/payments"+tc.query, &got)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("secondary widgets", func(t *testing.T) { testSecondaryWidgets(t, env) })

	t.Run("bracketed staff_ids is rejected instead of returning every schedule", func(t *testing.T) {
		status, body := env.getStatus(t, "/api/v1/dashboard/jobs?staff_ids[]=1")
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Contains(t, body, `unknown query parameter \"staff_ids[]\"`)
	})

	t.Run("empty range keeps every status row", func(t *testing.T) {
		var got dto.BreakdownResponse
		env.before2026(t).getJSON(t, "/api/v1/dashboard/estimates?range=last_month", &got)
		assert.Equal(t, zeroRows("draft", "pending", "won", "invoiced", "lost"), got.Rows)
		assert.Equal(t, "0.00", got.TotalAmount)
	})
}
