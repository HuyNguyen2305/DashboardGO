package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/model"
	"dashboardgo/internal/service"
	"dashboardgo/tests/mocks"
)

func hcm(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	return loc
}

func resolver(t *testing.T) *daterange.Resolver {
	t.Helper()
	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, hcm(t))
	return daterange.NewResolver(hcm(t), func() time.Time { return now })
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func keys(rows []model.BreakdownRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Key
	}
	return out
}

func TestJobsWidget(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)
	wantQuery := model.JobsQuery{Start: r.Start, End: r.End, TZ: "Asia/Ho_Chi_Minh", StaffIDs: []int64{1, 2}}

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().JobStatusBreakdown(mock.Anything, wantQuery).Return([]model.BreakdownRow{
		{Key: "terminated", Count: 1, Amount: dec("65.00")},
		{Key: "incomplete", Count: 18, Amount: dec("1371.24")},
		{Key: "completed", Count: 14, Amount: dec("924.30")},
	}, nil).Once()
	repo.EXPECT().JobDailyCounts(mock.Anything, wantQuery).Return([]model.DayStatusCount{
		{Day: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Status: "completed", Count: 1},
		{Day: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Status: "incomplete", Count: 2},
		{Day: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Status: "terminated", Count: 1},
	}, nil).Once()

	w, err := service.NewDashboardService(repo).Jobs(context.Background(), r, []int64{1, 2})
	require.NoError(t, err)

	// Every status in dashboard order; percent is by count (18/33, 14/33, 1/33).
	assert.Equal(t, []model.BreakdownRow{
		{Key: "incomplete", Count: 18, Amount: dec("1371.24"), Percent: 54.55},
		{Key: "completed", Count: 14, Amount: dec("924.30"), Percent: 42.42},
		{Key: "reschedule", Amount: decimal.Decimal{}},
		{Key: "canceled", Amount: decimal.Decimal{}},
		{Key: "terminated", Count: 1, Amount: dec("65.00"), Percent: 3.03},
		{Key: "workpool", Amount: decimal.Decimal{}},
	}, w.Rows)
	assert.Equal(t, int64(33), w.TotalCount)
	assert.True(t, w.TotalAmount.Equal(dec("2360.54")), w.TotalAmount.String())

	// One point per day of October, with every status present.
	require.Len(t, w.Daily, 31)
	assert.Equal(t, "2026-10-01", w.Daily[0].Date.Format("2006-01-02"))
	assert.Equal(t, map[string]int64{"incomplete": 0, "completed": 0, "reschedule": 0, "canceled": 0, "terminated": 0, "workpool": 0}, w.Daily[0].Counts)
	assert.Equal(t, int64(1), w.Daily[1].Counts["completed"])
	assert.Equal(t, int64(2), w.Daily[1].Counts["incomplete"])
	assert.Equal(t, int64(1), w.Daily[30].Counts["terminated"])
}

func TestDocumentWidgetsUseAmountPercentAndOrder(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	cases := []struct {
		name     string
		docType  string
		call     func(*service.DashboardService) (model.Breakdown, error)
		rows     []model.BreakdownRow
		wantKeys []string
		wantPct  map[string]float64
	}{
		{
			name:    "invoices",
			docType: "invoice",
			call: func(s *service.DashboardService) (model.Breakdown, error) {
				return s.Invoices(context.Background(), r)
			},
			rows: []model.BreakdownRow{
				{Key: "paid", Count: 13, Amount: dec("932.26")},
				{Key: "draft", Count: 21, Amount: dec("1378.64")},
				{Key: "void", Count: 1, Amount: dec("65.00")},
				{Key: "sent", Count: 8, Amount: dec("555.14")},
			},
			wantKeys: []string{"draft", "void", "sent", "partial", "partial_paid", "paid", "write_off", "failed"},
			// Screenshot: 47%, 2%, 19%, 32% — by amount, not by count.
			wantPct: map[string]float64{"draft": 47.04, "void": 2.22, "sent": 18.94, "paid": 31.81, "partial": 0},
		},
		{
			name:    "estimates",
			docType: "estimate",
			call: func(s *service.DashboardService) (model.Breakdown, error) {
				return s.Estimates(context.Background(), r)
			},
			rows: []model.BreakdownRow{
				{Key: "pending", Count: 3, Amount: dec("62.00")},
				{Key: "draft", Count: 16, Amount: dec("660.22")},
			},
			wantKeys: []string{"draft", "pending", "won", "invoiced", "lost"},
			wantPct:  map[string]float64{"draft": 91.42, "pending": 8.58, "lost": 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockDashboardRepository(t)
			repo.EXPECT().DocumentStatusBreakdown(mock.Anything, model.DocumentsQuery{
				DocType: tc.docType, Start: r.Start, End: r.End,
			}).Return(tc.rows, nil).Once()

			b, err := tc.call(service.NewDashboardService(repo))
			require.NoError(t, err)
			assert.Equal(t, tc.wantKeys, keys(b.Rows))
			for _, row := range b.Rows {
				if want, ok := tc.wantPct[row.Key]; ok {
					assert.InDelta(t, want, row.Percent, 0.001, row.Key)
				}
			}
		})
	}
}

func TestBreakdownKeepsUnexpectedKeys(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().DocumentStatusBreakdown(mock.Anything, mock.Anything).Return([]model.BreakdownRow{
		{Key: "archived", Count: 1, Amount: dec("10")},
		{Key: "paid", Count: 1, Amount: dec("30")},
	}, nil).Once()

	b, err := service.NewDashboardService(repo).Invoices(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, "archived", b.Rows[len(b.Rows)-1].Key, "a status the dashboard doesn't know must not be dropped")
	assert.True(t, b.TotalAmount.Equal(dec("40")))
}

func TestEmptyBreakdownHasZeroPercents(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().DocumentStatusBreakdown(mock.Anything, mock.Anything).Return(nil, nil).Once()

	b, err := service.NewDashboardService(repo).Estimates(context.Background(), r)
	require.NoError(t, err)
	assert.Len(t, b.Rows, 5)
	assert.Zero(t, b.TotalCount)
	assert.True(t, b.TotalAmount.IsZero())
	for _, row := range b.Rows {
		assert.Zero(t, row.Percent, row.Key)
	}
}

func TestInvoiceAgingWidget(t *testing.T) {
	asOf := resolver(t).Today()

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().InvoiceAging(mock.Anything, model.AgingQuery{
		AsOf: asOf, TZ: "Asia/Ho_Chi_Minh", Statuses: []string{"sent", "partial", "partial_paid"},
	}).Return([]model.BreakdownRow{
		{Key: "61_90", Count: 1, Amount: dec("95.00")},
		{Key: "0_30", Count: 11, Amount: dec("647.91")},
	}, nil).Once()

	w, err := service.NewDashboardService(repo).InvoiceAging(context.Background(), asOf)
	require.NoError(t, err)
	assert.Equal(t, asOf, w.AsOf)
	assert.Equal(t, []string{"0_30", "31_60", "61_90", "90_plus"}, keys(w.Rows))
	// Screenshot: 87% and 13%.
	assert.InDelta(t, 87.21, w.Rows[0].Percent, 0.001)
	assert.InDelta(t, 12.79, w.Rows[2].Percent, 0.001)
	assert.True(t, w.TotalAmount.Equal(dec("742.91")))
	assert.Equal(t, int64(12), w.TotalCount)
}

func TestYearSeriesWidgets(t *testing.T) {
	r, err := resolver(t).Year(2026)
	require.NoError(t, err)
	wantQuery := model.MonthlyQuery{
		Start: r.Start, End: r.End, TZ: "Asia/Ho_Chi_Minh",
		RevenueStatuses:          []string{"sent", "partial", "partial_paid", "paid"},
		ForecastEstimateStatuses: []string{"draft", "pending", "won", "invoiced"},
		ForecastInvoiceStatuses:  []string{"sent", "partial", "partial_paid", "paid"},
	}
	repoMonths := []model.MonthlyAmount{
		{Month: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Amount: dec("380.00")},
		{Month: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Amount: dec("830.00")},
	}

	cases := []struct {
		name   string
		expect func(*mocks.MockDashboardRepository)
		call   func(*service.DashboardService) (model.YearSeries, error)
	}{
		{
			"revenue per month",
			func(m *mocks.MockDashboardRepository) {
				m.EXPECT().MonthlyRevenue(mock.Anything, wantQuery).Return(repoMonths, nil).Once()
			},
			func(s *service.DashboardService) (model.YearSeries, error) {
				return s.RevenuePerMonth(context.Background(), r)
			},
		},
		{
			"sales forecast",
			func(m *mocks.MockDashboardRepository) {
				m.EXPECT().MonthlyForecast(mock.Anything, wantQuery).Return(repoMonths, nil).Once()
			},
			func(s *service.DashboardService) (model.YearSeries, error) {
				return s.SalesForecast(context.Background(), r)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockDashboardRepository(t)
			tc.expect(repo)

			s, err := tc.call(service.NewDashboardService(repo))
			require.NoError(t, err)
			require.Len(t, s.Months, 12)
			assert.Equal(t, "2026-01", s.Months[0].Month.Format("2006-01"))
			assert.True(t, s.Months[0].Amount.IsZero())
			assert.True(t, s.Months[7].Amount.Equal(dec("380.00")))
			assert.True(t, s.Months[9].Amount.Equal(dec("830.00")))
			assert.True(t, s.Months[11].Amount.IsZero())
			assert.True(t, s.Total.Equal(dec("1210.00")), s.Total.String())
		})
	}
}

func TestPaymentsWidgetKeepsRepositoryOrder(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().PaymentsByMethod(mock.Anything, model.PeriodQuery{Start: r.Start, End: r.End}).Return([]model.BreakdownRow{
		{Key: "check", Count: 11, Amount: dec("715.00")},
		{Key: "stripe", Count: 1, Amount: dec("152.26")},
	}, nil).Once()

	b, err := service.NewDashboardService(repo).Payments(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, []string{"check", "stripe"}, keys(b.Rows))
	// Screenshot: 82% and 18%.
	assert.InDelta(t, 82.44, b.Rows[0].Percent, 0.001)
	assert.InDelta(t, 17.56, b.Rows[1].Percent, 0.001)
}

func TestWidgetErrorsAreWrapped(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)
	dbErr := errors.New("timeout")

	cases := []struct {
		name   string
		expect func(*mocks.MockDashboardRepository)
		call   func(*service.DashboardService) error
	}{
		{"jobs breakdown", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().JobStatusBreakdown(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error { _, err := s.Jobs(context.Background(), r, nil); return err }},
		{"jobs daily", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().JobStatusBreakdown(mock.Anything, mock.Anything).Return(nil, nil).Once()
			m.EXPECT().JobDailyCounts(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error { _, err := s.Jobs(context.Background(), r, nil); return err }},
		{"invoices", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().DocumentStatusBreakdown(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error { _, err := s.Invoices(context.Background(), r); return err }},
		{"aging", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().InvoiceAging(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.InvoiceAging(context.Background(), r.Start)
			return err
		}},
		{"revenue per month", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().MonthlyRevenue(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.RevenuePerMonth(context.Background(), r)
			return err
		}},
		{"sales forecast", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().MonthlyForecast(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.SalesForecast(context.Background(), r)
			return err
		}},
		{"payments", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().PaymentsByMethod(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error { _, err := s.Payments(context.Background(), r); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockDashboardRepository(t)
			tc.expect(repo)
			assert.ErrorIs(t, tc.call(service.NewDashboardService(repo)), dbErr)
		})
	}
}
