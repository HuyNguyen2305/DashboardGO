package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/model"
	"dashboardgo/internal/service"
	"dashboardgo/tests/mocks"
)

func month(m time.Month) time.Time { return time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC) }

// expectForecastTx makes repo run fn directly and requires the read-only snapshot.
func expectForecastTx(repo *mocks.MockDashboardRepository) {
	opts := mock.MatchedBy(func(o model.TxOptions) bool { return o.ReadOnly && o.Snapshot })
	repo.EXPECT().WithTx(mock.Anything, opts, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ model.TxOptions, fn func(context.Context) error) error { return fn(ctx) }).Once()
}

func TestSalesForecastZipsBookedAndRecurring(t *testing.T) {
	r, err := resolver(t).Year(2026)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	expectForecastTx(repo)
	wantQuery := model.MonthlyQuery{
		Start: r.Start, End: r.End, TZ: "Asia/Ho_Chi_Minh",
		RevenueStatuses: model.RevenueInvoiceStatuses,
		JobStatuses:     model.BookedJobStatuses,
	}
	repo.EXPECT().MonthlyForecast(mock.Anything, wantQuery).Return([]model.MonthlyAmount{
		{Month: month(time.August), Amount: dec("380.00")},
		{Month: month(time.October), Amount: dec("830.00")},
	}, nil).Once()
	rec := mocks.NewMockRecurringForecaster(t)
	rec.EXPECT().MonthlyRecurring(mock.Anything, model.Window{From: r.Start, To: r.End}).Return([]model.MonthlyAmount{
		{Month: month(time.October), Amount: dec("100.00")},
		{Month: month(time.November), Amount: dec("250.50")},
	}, nil).Once()

	got, err := service.NewDashboardService(repo).WithRecurring(rec).SalesForecast(context.Background(), r)
	require.NoError(t, err)
	require.Len(t, got.Months, 12)
	assert.True(t, got.Months[7].Booked.Equal(dec("380.00")))
	assert.True(t, got.Months[7].Recurring.IsZero())
	assert.True(t, got.Months[9].Booked.Equal(dec("830.00")))
	assert.True(t, got.Months[9].Recurring.Equal(dec("100.00")))
	assert.True(t, got.Months[10].Booked.IsZero())
	assert.True(t, got.Months[10].Recurring.Equal(dec("250.50")))
	assert.Equal(t, "2026-11", got.Months[10].Month.Format("2006-01"))
	assert.True(t, got.BookedTotal.Equal(dec("1210.00")), got.BookedTotal.String())
	assert.True(t, got.RecurringTotal.Equal(dec("350.50")), got.RecurringTotal.String())
}

func TestSalesForecastWithoutForecasterHasZeroRecurring(t *testing.T) {
	r, err := resolver(t).Year(2026)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	expectForecastTx(repo)
	repo.EXPECT().MonthlyForecast(mock.Anything, mock.Anything).Return([]model.MonthlyAmount{
		{Month: month(time.October), Amount: dec("830.00")},
	}, nil).Once()

	got, err := service.NewDashboardService(repo).SalesForecast(context.Background(), r)
	require.NoError(t, err)
	require.Len(t, got.Months, 12)
	assert.True(t, got.Months[9].Booked.Equal(dec("830.00")))
	for _, m := range got.Months {
		assert.True(t, m.Recurring.IsZero())
	}
	assert.True(t, got.RecurringTotal.IsZero())
}

func TestSalesForecastForecasterErrorIsWrapped(t *testing.T) {
	r, err := resolver(t).Year(2026)
	require.NoError(t, err)
	boom := errors.New("boom")

	repo := mocks.NewMockDashboardRepository(t)
	expectForecastTx(repo)
	repo.EXPECT().MonthlyForecast(mock.Anything, mock.Anything).Return(nil, nil).Once()
	rec := mocks.NewMockRecurringForecaster(t)
	rec.EXPECT().MonthlyRecurring(mock.Anything, mock.Anything).Return(nil, boom).Once()

	_, err = service.NewDashboardService(repo).WithRecurring(rec).SalesForecast(context.Background(), r)
	require.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, "sales forecast widget: ")
}
