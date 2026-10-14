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

func octoberRange() daterange.Range {
	loc := time.FixedZone("GMT+7", 7*60*60)
	return daterange.Range{
		Preset: daterange.ThisMonth,
		Start:  time.Date(2026, time.October, 1, 0, 0, 0, 0, loc),
		End:    time.Date(2026, time.November, 1, 0, 0, 0, 0, loc),
	}
}

func TestStatsPassesRangeAndStatusRules(t *testing.T) {
	r := octoberRange()
	want := model.DashboardStats{
		Revenue:       decimal.RequireFromString("1487.40"),
		Collected:     decimal.RequireFromString("867.26"),
		JobsCompleted: 14,
	}

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().Stats(mock.Anything, model.StatsQuery{
		Start:                   r.Start,
		End:                     r.End,
		RevenueStatuses:         []string{"sent", "partial_paid", "paid"},
		NewJobsEstimateStatuses: []string{"draft", "pending", "won", "invoiced"},
		NewJobsInvoiceStatuses:  []string{"sent", "partial_paid", "paid"},
		OnlineJobStatuses:       []string{"incomplete", "completed", "reschedule", "workpool"},
	}).Return(want, nil).Once()

	got, err := service.NewDashboardService(repo).Stats(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestStatsWrapsRepositoryError(t *testing.T) {
	dbErr := errors.New("connection reset")

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().Stats(mock.Anything, mock.Anything).Return(model.DashboardStats{}, dbErr).Once()

	_, err := service.NewDashboardService(repo).Stats(context.Background(), octoberRange())
	require.ErrorIs(t, err, dbErr)
	assert.Contains(t, err.Error(), "dashboard stats")
}

func TestStatsPassesCallerContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "request-scoped")

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().Stats(mock.Anything, mock.Anything).
		Run(func(got context.Context, _ model.StatsQuery) {
			assert.Equal(t, "request-scoped", got.Value(ctxKey{}))
		}).
		Return(model.DashboardStats{}, nil).Once()

	_, err := service.NewDashboardService(repo).Stats(ctx, octoberRange())
	require.NoError(t, err)
}
