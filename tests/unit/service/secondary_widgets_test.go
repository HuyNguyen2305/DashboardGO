package service_test

import (
	"context"
	"errors"
	"fmt"
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

func ptr[T any](v T) *T { return &v }

func TestRevenueByStaff(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().RevenueByStaff(mock.Anything, model.StatsQuery{
		Start: r.Start, End: r.End, RevenueStatuses: []string{"sent", "partial_paid", "paid"},
	}).Return([]model.StaffRevenueRow{
		{StaffID: ptr(int64(1)), Name: "Thọ", Count: 7, Amount: dec("955.00")},
		{StaffID: nil, Count: 4, Amount: dec("444.20")},
		{StaffID: ptr(int64(5)), Name: "thon nguyen", Count: 1, Amount: dec("1.00")},
	}, nil).Once()

	w, err := service.NewDashboardService(repo).RevenueByStaff(context.Background(), r)
	require.NoError(t, err)

	// Screenshot: $1,400.20 split 68% / 32% / 0%, with "Other" kept where its amount places it.
	assert.True(t, w.TotalAmount.Equal(dec("1400.20")), w.TotalAmount.String())
	require.Len(t, w.Rows, 3)
	assert.Equal(t, "Thọ", w.Rows[0].Name)
	assert.InDelta(t, 68.21, w.Rows[0].Percent, 0.001) // 68.20 + 31.72 + 0.07 was 99.99
	assert.Nil(t, w.Rows[1].StaffID)
	assert.Equal(t, "Other", w.Rows[1].Name)
	assert.InDelta(t, 31.72, w.Rows[1].Percent, 0.001)
	assert.InDelta(t, 0.07, w.Rows[2].Percent, 0.001)
}

func TestActiveServices(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)

	q := model.ServicesQuery{Start: r.Start, End: r.End, Statuses: []string{"incomplete"}}
	repo := mocks.NewMockDashboardRepository(t)
	expectSnapshot(repo)
	repo.EXPECT().ActiveServiceCustomers(mock.Anything, q).
		Return(model.ServiceCustomers{CustomerCount: 9, RecurringCustomerCount: 3}, nil).Once()
	repo.EXPECT().ActiveServices(mock.Anything, q).Return([]model.ServiceRow{
		{ServiceID: ptr(int64(1)), Name: "Blank Service", Count: 4, CustomerCount: 2, Amount: dec("594.24")},
		{ServiceID: ptr(int64(2)), Name: "Initial Service", Count: 3, Amount: dec("285.00")},
		{ServiceID: ptr(int64(3)), Name: "Bi-Monthly Service", IsRecurring: true, Count: 4, Amount: dec("260.00")},
		{ServiceID: ptr(int64(4)), Name: "Every 21 Days", IsRecurring: true, Count: 2, Amount: dec("120.00")},
		{ServiceID: ptr(int64(5)), Name: "Blank Service 123", Count: 4, Amount: dec("112.00")},
		{ServiceID: nil, Count: 1, Amount: dec("0.00")},
	}, nil).Once()

	w, err := service.NewDashboardService(repo).ActiveServices(context.Background(), r)
	require.NoError(t, err)

	// Screenshot: 18 active, 6 recurring, $1,371.24, Blank Service 43.34%.
	assert.Equal(t, int64(18), w.TotalCount)
	assert.Equal(t, int64(6), w.RecurringCount)
	assert.Equal(t, model.ServiceCustomers{CustomerCount: 9, RecurringCustomerCount: 3}, w.ServiceCustomers)
	assert.Equal(t, int64(2), w.Rows[0].CustomerCount)
	assert.True(t, w.TotalAmount.Equal(dec("1371.24")), w.TotalAmount.String())
	assert.InDelta(t, 43.34, w.Rows[0].Percent, 0.001)
	assert.InDelta(t, 20.78, w.Rows[1].Percent, 0.001)
	assert.Equal(t, "No service", w.Rows[5].Name)
	assert.Zero(t, w.Rows[5].Percent)
}

func TestTags(t *testing.T) {
	t.Run("percent is share of tag uses", func(t *testing.T) {
		repo := mocks.NewMockDashboardRepository(t)
		repo.EXPECT().TagUsage(mock.Anything, "location").Return([]model.TagRow{
			{TagID: ptr(int64(1)), Name: "1233", Count: 1},
			{TagID: ptr(int64(2)), Name: "a456", Count: 1},
		}, nil).Once()

		w, err := service.NewDashboardService(repo).Tags(context.Background(), "location")
		require.NoError(t, err)
		assert.Equal(t, "location", w.Scope)
		assert.Equal(t, int64(2), w.TotalCount)
		assert.InDelta(t, 50.0, w.Rows[0].Percent, 0.001)
		assert.InDelta(t, 50.0, w.Rows[1].Percent, 0.001)
	})

	t.Run("no tags in use", func(t *testing.T) {
		repo := mocks.NewMockDashboardRepository(t)
		repo.EXPECT().TagUsage(mock.Anything, "customer").Return(nil, nil).Once()

		w, err := service.NewDashboardService(repo).Tags(context.Background(), "customer")
		require.NoError(t, err)
		assert.Empty(t, w.Rows)
		assert.NotNil(t, w.Rows)
		assert.Zero(t, w.TotalCount)
	})

	t.Run("unknown scope never reaches the repository", func(t *testing.T) {
		repo := mocks.NewMockDashboardRepository(t) // no expectations
		for _, scope := range []string{"", "staff", "Location"} {
			_, err := service.NewDashboardService(repo).Tags(context.Background(), scope)
			assert.ErrorIs(t, err, model.ErrInvalidScope, "scope %q", scope)
		}
	})
}

func TestCustomersBySource(t *testing.T) {
	r, err := resolver(t).Year(2026)
	require.NoError(t, err)

	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().CustomersBySource(mock.Anything, model.MonthlyQuery{Start: r.Start, End: r.End, TZ: "Asia/Ho_Chi_Minh"}).
		Return([]model.SourceMonthCount{
			{Month: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Source: "na", Count: 21},
			{Month: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Source: "facebook", Count: 1},
			{Month: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Source: "na", Count: 10},
			{Month: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Source: "tiktok", Count: 2},
		}, nil).Once()

	s, err := service.NewDashboardService(repo).CustomersBySource(context.Background(), r)
	require.NoError(t, err)

	// Legend order from the screenshot, then sources the dashboard doesn't know yet.
	assert.Equal(t, []string{"yelp", "angieslist", "craigslist", "facebook", "google", "referral", "na", "tiktok"}, s.Sources)
	assert.Equal(t, int64(34), s.TotalCount)
	require.Len(t, s.Months, 12)
	assert.Len(t, s.Months[0].Counts, 8, "every month lists every source")
	assert.Zero(t, s.Months[0].Counts["na"])
	assert.Equal(t, int64(21), s.Months[8].Counts["na"])
	assert.Equal(t, int64(1), s.Months[9].Counts["facebook"])
	assert.Equal(t, int64(10), s.Months[9].Counts["na"])
	assert.Equal(t, int64(2), s.Months[9].Counts["tiktok"])
}

func TestSecondaryWidgetErrorsAreWrapped(t *testing.T) {
	r, err := resolver(t).Preset(daterange.ThisMonth)
	require.NoError(t, err)
	dbErr := errors.New("timeout")

	cases := []struct {
		name   string
		expect func(*mocks.MockDashboardRepository)
		call   func(*service.DashboardService) error
	}{
		{"revenue by staff", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().RevenueByStaff(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.RevenueByStaff(context.Background(), r)
			return err
		}},
		{"active services", func(m *mocks.MockDashboardRepository) {
			expectSnapshot(m)
			m.EXPECT().ActiveServices(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.ActiveServices(context.Background(), r)
			return err
		}},
		{"active service customers", func(m *mocks.MockDashboardRepository) {
			expectSnapshot(m)
			m.EXPECT().ActiveServices(mock.Anything, mock.Anything).Return(nil, nil).Once()
			m.EXPECT().ActiveServiceCustomers(mock.Anything, mock.Anything).Return(model.ServiceCustomers{}, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.ActiveServices(context.Background(), r)
			return err
		}},
		{"tags", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().TagUsage(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.Tags(context.Background(), "location")
			return err
		}},
		{"customers by source", func(m *mocks.MockDashboardRepository) {
			m.EXPECT().CustomersBySource(mock.Anything, mock.Anything).Return(nil, dbErr).Once()
		}, func(s *service.DashboardService) error {
			_, err := s.CustomersBySource(context.Background(), r)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockDashboardRepository(t)
			tc.expect(repo)
			assert.ErrorIs(t, tc.call(service.NewDashboardService(repo)), dbErr)
		})
	}
}

func TestTagsFoldTheTailIntoOtherTags(t *testing.T) {
	// 52 tags: the 50 most used are listed, the last two become "Other tags".
	rows := make([]model.TagRow, 52)
	for i := range rows {
		rows[i] = model.TagRow{TagID: ptr(int64(i + 1)), Name: fmt.Sprintf("tag-%02d", i+1), Count: int64(60 - i)}
	}
	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().TagUsage(mock.Anything, "customer").Return(rows, nil).Once()

	w, err := service.NewDashboardService(repo).Tags(context.Background(), "customer")
	require.NoError(t, err)

	require.Len(t, w.Rows, model.MaxTagRows+1)
	other := w.Rows[model.MaxTagRows]
	assert.Nil(t, other.TagID)
	assert.Equal(t, service.OtherTagsLabel, other.Name)
	assert.Equal(t, int64(10+9), other.Count, "tag-51 (10) + tag-52 (9)")
	assert.Equal(t, "tag-50", w.Rows[model.MaxTagRows-1].Name)

	var total int64
	sum := decimal.Zero
	for _, r := range w.Rows {
		total += r.Count
		sum = sum.Add(decimal.NewFromFloat(r.Percent))
	}
	assert.Equal(t, w.TotalCount, total, "no uses are lost")
	assert.True(t, sum.Equal(decimal.NewFromInt(100)), "percents sum to %s", sum)
}

func TestTagsUnderTheCapHaveNoOtherRow(t *testing.T) {
	repo := mocks.NewMockDashboardRepository(t)
	repo.EXPECT().TagUsage(mock.Anything, "location").Return([]model.TagRow{{TagID: ptr(int64(1)), Name: "a", Count: 1}}, nil).Once()

	w, err := service.NewDashboardService(repo).Tags(context.Background(), "location")
	require.NoError(t, err)
	assert.Len(t, w.Rows, 1)
}
