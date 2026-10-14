//go:build integration

package dashboard_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/dto"
)

// serviceID looks up a seeded service's ID by name.
func (e testEnv) serviceID(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT id FROM services WHERE name = $1`, name).Scan(&id))
	return id
}

func ptr[T any](v T) *T { return &v }

// testSecondaryWidgets runs against the same seeded environment as TestDashboardWidgets.
func testSecondaryWidgets(t *testing.T, env testEnv) {
	october := dto.DateRange{Preset: "this_month", Start: "2026-10-01", End: "2026-10-31"}

	t.Run("revenue by staff", func(t *testing.T) {
		var got dto.StaffRevenueResponse
		env.getJSON(t, "/api/v1/dashboard/revenue-by-staff", &got)

		// Total equals the Revenue stat. "Other" = INV-1 (no job) + INV-6 (job without staff).
		assert.Equal(t, dto.StaffRevenueResponse{
			Range:       october,
			TotalAmount: "1487.40",
			Rows: []dto.StaffRevenueRow{
				{StaffID: ptr(env.staffID(t, "Thọ")), Name: "Thọ", Count: 1, Amount: "892.26", Percent: 59.99},
				{StaffID: nil, Name: "Other", Count: 2, Amount: "595.14", Percent: 40.01},
			},
		}, got)
	})

	t.Run("active services", func(t *testing.T) {
		var got dto.ActiveServicesResponse
		env.getJSON(t, "/api/v1/dashboard/active-services", &got)

		// Screenshot: 18 active, 6 recurring, footer $1,371.24 = the Jobs widget's Incomplete row.
		assert.Equal(t, dto.ActiveServicesResponse{
			Range:          october,
			TotalCount:     18,
			RecurringCount: 6,
			// Every seeded job belongs to the one seed customer.
			CustomerCount:          1,
			RecurringCustomerCount: 1,
			TotalAmount:            "1371.24",
			Rows: []dto.ServiceRow{
				{ServiceID: ptr(env.serviceID(t, "Blank Service")), Name: "Blank Service", Count: 8, CustomerCount: 1, Amount: "587.52", Percent: 42.85},
				{ServiceID: ptr(env.serviceID(t, "Bi-Monthly Service")), Name: "Bi-Monthly Service", IsRecurring: true, Count: 4, CustomerCount: 1, Amount: "293.76", Percent: 21.42},
				{ServiceID: ptr(env.serviceID(t, "Initial Service")), Name: "Initial Service", Count: 3, CustomerCount: 1, Amount: "266.90", Percent: 19.46},
				{ServiceID: ptr(env.serviceID(t, "Every 21 Days")), Name: "Every 21 Days", IsRecurring: true, Count: 2, CustomerCount: 1, Amount: "146.88", Percent: 10.71},
				{ServiceID: nil, Name: "No service", Count: 1, CustomerCount: 1, Amount: "76.18", Percent: 5.56},
			},
		}, got)
	})

	t.Run("active services, no jobs in range", func(t *testing.T) {
		var got dto.ActiveServicesResponse
		env.getJSON(t, "/api/v1/dashboard/active-services?range=last_month", &got)
		assert.Equal(t, int64(0), got.TotalCount)
		assert.Equal(t, "0.00", got.TotalAmount)
		assert.Equal(t, []dto.ServiceRow{}, got.Rows)
	})

	t.Run("tags", func(t *testing.T) {
		cases := []struct {
			name  string
			query string
			want  []dto.TagRow
			total int64
		}{
			// Screenshot: 1233 and a456 at 50% each; the unused tag is not listed.
			{"location (default)", "", []dto.TagRow{
				{Name: "1233", Count: 1, CustomerCount: ptr(int64(1)), Percent: 50},
				{Name: "a456", Count: 1, CustomerCount: ptr(int64(1)), Percent: 50},
			}, 2},
			{"empty scope means location", "?scope=", []dto.TagRow{
				{Name: "1233", Count: 1, CustomerCount: ptr(int64(1)), Percent: 50},
				{Name: "a456", Count: 1, CustomerCount: ptr(int64(1)), Percent: 50},
			}, 2},
			{"customer", "?scope=customer", []dto.TagRow{{Name: "vip", Count: 2, Percent: 66.67}, {Name: "late-payer", Count: 1, Percent: 33.33}}, 3},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var got dto.TagsResponse
				env.getJSON(t, "/api/v1/dashboard/tags"+tc.query, &got)
				assert.Equal(t, tc.total, got.TotalCount)
				require.Len(t, got.Rows, len(tc.want))
				for i, want := range tc.want {
					require.NotNil(t, got.Rows[i].TagID)
					got.Rows[i].TagID = nil
					assert.Equal(t, want, got.Rows[i])
				}
			})
		}
	})

	t.Run("customers by source", func(t *testing.T) {
		var got dto.CustomersBySourceResponse
		env.getJSON(t, "/api/v1/dashboard/customers-by-source?year=2026", &got)

		assert.Equal(t, dto.DateRange{Preset: "year", Start: "2026-01-01", End: "2026-12-31"}, got.Range)
		assert.Equal(t, []string{"yelp", "angieslist", "craigslist", "facebook", "google", "referral", "na"}, got.Sources)
		assert.Equal(t, int64(36), got.TotalCount, "the December 2025 customer must not count")
		require.Len(t, got.Months, 12)

		byMonth := make(map[string]map[string]int64, 12)
		for _, m := range got.Months {
			assert.Len(t, m.Counts, 7, m.Month)
			byMonth[m.Month] = m.Counts
		}
		assert.Equal(t, int64(1), byMonth["2026-01"]["na"])
		assert.Equal(t, int64(2), byMonth["2026-06"]["na"])
		assert.Equal(t, int64(0), byMonth["2026-07"]["google"], "Aug 1 00:30 local must not fall into July")
		assert.Equal(t, int64(1), byMonth["2026-08"]["google"])
		assert.Equal(t, int64(21), byMonth["2026-09"]["na"])
		assert.Equal(t, int64(10), byMonth["2026-10"]["na"])
		assert.Equal(t, int64(1), byMonth["2026-10"]["facebook"])
		assert.Equal(t, int64(0), byMonth["2026-12"]["yelp"])
	})
}
