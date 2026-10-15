package devdata_test

import (
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/devdata"
	"dashboardgo/internal/recurrence"
)

func today(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	return time.Date(2026, time.October, 12, 0, 0, 0, 0, loc)
}

func demo(t *testing.T, months int, seed uint64) *devdata.Dataset {
	t.Helper()
	ds, err := devdata.Demo(devdata.DemoOptions{Months: months, Seed: seed, Today: today(t)})
	require.NoError(t, err)
	return ds
}

func TestDemoIsReproducible(t *testing.T) {
	a, b := demo(t, 6, 7), demo(t, 6, 7)
	assert.Equal(t, a, b, "the same seed must give the same data")

	c := demo(t, 6, 8)
	assert.NotEqual(t, a.Documents, c.Documents, "a different seed should give different data")
}

func TestDemoMonthsBounds(t *testing.T) {
	for _, months := range []int{0, -1, 37} {
		_, err := devdata.Demo(devdata.DemoOptions{Months: months, Seed: 1, Today: today(t)})
		assert.ErrorIs(t, err, devdata.ErrInvalidMonths, "months=%d", months)
	}
	for _, months := range []int{devdata.MinDemoMonths, devdata.MaxDemoMonths} {
		_, err := devdata.Demo(devdata.DemoOptions{Months: months, Seed: 1, Today: today(t)})
		assert.NoError(t, err, "months=%d", months)
	}
}

func TestDemoRespectsSchemaRules(t *testing.T) {
	ds := demo(t, 12, 42)
	now := today(t).AddDate(0, 0, 1) // end of "today"
	windowStart := time.Date(2025, time.November, 1, 0, 0, 0, 0, today(t).Location())

	require.NotEmpty(t, ds.Customers)
	require.NotEmpty(t, ds.Jobs)
	require.NotEmpty(t, ds.Payments)

	sources := []string{"yelp", "angieslist", "craigslist", "facebook", "google", "referral"}
	for _, c := range ds.Customers {
		assert.Contains(t, []string{"lead", "active", "inactive"}, c.Status)
		if c.Source != nil {
			assert.Contains(t, sources, *c.Source)
		}
		assert.False(t, c.CreatedAt.Before(windowStart), "customer %d created before the window", c.ID)
		assert.True(t, c.CreatedAt.Before(now), "customer %d created in the future", c.ID)
	}

	jobStatuses := []string{"incomplete", "completed", "reschedule", "canceled", "terminated", "workpool"}
	for _, j := range ds.Jobs {
		assert.Contains(t, jobStatuses, j.Status)
		assert.Equal(t, j.Status == "completed", j.CompletedAt != nil, "job %d: completed_at only on completed jobs", j.ID)
		assert.False(t, j.CreatedAt.After(j.ScheduledAt), "job %d created after it was scheduled", j.ID)
		assert.True(t, j.Total.GreaterThanOrEqual(decimal.Zero))
	}

	invoiceStatuses := []string{"draft", "sent", "partial_paid", "paid", "void", "write_off", "failed"}
	estimateStatuses := []string{"draft", "pending", "won", "invoiced", "lost"}
	docs := make(map[int64]devdata.Document, len(ds.Documents))
	numbers := make(map[string]bool)
	for _, d := range ds.Documents {
		docs[d.ID] = d
		key := d.DocType + "/" + d.Number
		assert.False(t, numbers[key], "duplicate document number %s", key)
		numbers[key] = true
		assert.True(t, d.IssuedAt.Before(now), "document %s issued in the future", d.Number)

		switch d.DocType {
		case "invoice":
			assert.Contains(t, invoiceStatuses, d.Status)
			assert.NotNil(t, d.DueDate)
			assert.Nil(t, d.ExpiresAt)
			assert.True(t, d.AmountPaid.LessThanOrEqual(d.Total), "invoice %s overpaid", d.Number)
		case "estimate":
			assert.Contains(t, estimateStatuses, d.Status)
			assert.Nil(t, d.DueDate, "estimates never have a due date")
			assert.True(t, d.AmountPaid.IsZero())
		default:
			t.Fatalf("unknown doc_type %q", d.DocType)
		}
		assert.True(t, d.Total.Equal(d.Subtotal.Add(d.Tax)))
	}

	// Converted invoices point at invoiced estimates, and each estimate is converted at most once.
	converted := make(map[int64]bool)
	for _, d := range ds.Documents {
		if d.SourceDocumentID == nil {
			continue
		}
		src, ok := docs[*d.SourceDocumentID]
		require.True(t, ok)
		assert.Equal(t, "invoice", d.DocType)
		assert.Equal(t, "estimate", src.DocType)
		assert.Equal(t, "invoiced", src.Status)
		assert.False(t, converted[src.ID], "estimate %s converted twice", src.Number)
		converted[src.ID] = true
	}

	// Payments only go to invoices, never exceed what the invoice says was paid, and fall within the window.
	paid := make(map[int64]decimal.Decimal)
	for _, p := range ds.Payments {
		d, ok := docs[p.DocumentID]
		require.True(t, ok)
		assert.Equal(t, "invoice", d.DocType)
		assert.Contains(t, []string{"check", "stripe", "card", "cash"}, p.Method)
		assert.True(t, p.Amount.GreaterThan(decimal.Zero))
		assert.False(t, p.ReceivedAt.Before(d.IssuedAt), "payment %d before its invoice", p.ID)
		assert.True(t, p.ReceivedAt.Before(now), "payment %d in the future", p.ID)
		paid[p.DocumentID] = paid[p.DocumentID].Add(p.Amount)
	}
	for id, sum := range paid {
		assert.True(t, sum.Equal(docs[id].AmountPaid), "payments for %s add up to %s, amount_paid is %s",
			docs[id].Number, sum, docs[id].AmountPaid)
	}

	// Tags stay within their scope.
	tagScope := make(map[int64]string)
	for _, tag := range ds.Tags {
		tagScope[tag.ID] = tag.Scope
	}
	for _, ct := range ds.CustomerTags {
		assert.Equal(t, "customer", tagScope[ct.TagID])
	}
	for _, lt := range ds.LocationTags {
		assert.Equal(t, "location", tagScope[lt.TagID])
	}
}

func TestDemoCoversEveryWidget(t *testing.T) {
	ds := demo(t, 12, 42)

	statuses := func(docType string) []string {
		var out []string
		for _, d := range ds.Documents {
			if d.DocType == docType && !slices.Contains(out, d.Status) {
				out = append(out, d.Status)
			}
		}
		return out
	}
	assert.Subset(t, statuses("invoice"), []string{"draft", "sent", "partial_paid", "paid", "void"})
	assert.Subset(t, statuses("estimate"), []string{"draft", "pending", "won", "invoiced", "lost"})

	var recurring, unassigned, online int
	for _, s := range ds.Services {
		if s.IsRecurring {
			recurring++
		}
	}
	for _, j := range ds.Jobs {
		if j.StaffID == nil {
			unassigned++
		}
		if j.BookedOnline {
			online++
		}
	}
	assert.Equal(t, 2, recurring)
	assert.Positive(t, unassigned, "some jobs need no staff for the Other row")
	assert.Positive(t, online, "some jobs should be booked online")
	assert.NotEmpty(t, ds.CustomerTags)
	assert.NotEmpty(t, ds.LocationTags)
}

func TestDemoNeverCompletesJobsInTheFuture(t *testing.T) {
	end := today(t).AddDate(0, 0, 1) // the generated data ends with "today"
	for seed := uint64(1); seed <= 20; seed++ {
		for _, j := range demo(t, 12, seed).Jobs {
			if j.Status != "completed" {
				assert.Nil(t, j.CompletedAt, "seed %d job %d: only completed jobs have completed_at", seed, j.ID)
				continue
			}
			require.NotNil(t, j.CompletedAt, "seed %d job %d: completed jobs need completed_at", seed, j.ID)
			assert.True(t, j.CompletedAt.Before(end), "seed %d job %d completed at %s, after the end of today", seed, j.ID, j.CompletedAt)
		}
	}
}

// planRule rebuilds the recurrence rule of a demo plan in the business timezone.
func planRule(p devdata.RecurringPlan, loc *time.Location) recurrence.Rule {
	y, m, d := p.StartDate.Date()
	r := recurrence.Rule{
		Unit:  recurrence.Unit(p.IntervalUnit),
		Count: p.IntervalCount,
		Start: time.Date(y, m, d, 0, 0, 0, 0, loc),
	}
	if p.EndDate != nil {
		y, m, d := p.EndDate.Date()
		end := time.Date(y, m, d, 0, 0, 0, 0, loc)
		r.End = &end
	}
	return r
}

func TestDemoRecurringPlans(t *testing.T) {
	ds := demo(t, 12, 42)
	loc := today(t).Location()
	todayY, todayM, todayD := today(t).Date()

	require.GreaterOrEqual(t, len(ds.RecurringPlans), 3)

	customerLocations := make(map[int64]int64, len(ds.Locations))
	for _, l := range ds.Locations {
		customerLocations[l.ID] = l.CustomerID
	}
	services := make(map[int64]devdata.Service, len(ds.Services))
	for _, s := range ds.Services {
		services[s.ID] = s
	}

	type cadence struct {
		unit  string
		count int
		day   int
	}
	var got []cadence
	plans := make(map[int64]devdata.RecurringPlan, len(ds.RecurringPlans))
	for _, p := range ds.RecurringPlans {
		plans[p.ID] = p
		rule := planRule(p, loc)
		require.NoError(t, rule.Validate(), "plan %d", p.ID)
		assert.Equal(t, "active", p.Status)
		assert.True(t, p.UnitPrice.Equal(services[p.ServiceID].Price), "plan %d unit price comes from the service", p.ID)
		if p.LocationID != nil {
			assert.Equal(t, p.CustomerID, customerLocations[*p.LocationID], "plan %d location belongs to its customer", p.ID)
		}
		assert.True(t, rule.At(0).Before(today(t)), "plan %d starts in the past", p.ID)
		got = append(got, cadence{p.IntervalUnit, p.IntervalCount, p.StartDate.Day()})
	}
	assert.True(t, slices.ContainsFunc(got, func(c cadence) bool { return c.unit == "week" && c.count == 1 }))
	assert.True(t, slices.ContainsFunc(got, func(c cadence) bool { return c.unit == "week" && c.count == 2 }))
	assert.True(t, slices.ContainsFunc(got, func(c cadence) bool { return c.unit == "month" && c.count == 1 && c.day == 31 }))

	type visit struct {
		plan int64
		date string
	}
	seen := make(map[visit]bool)
	linkedPerPlan := make(map[int64]int)
	for _, j := range ds.Jobs {
		if j.RecurringPlanID == nil {
			assert.Nil(t, j.OccurrenceDate, "job %d", j.ID)
			continue
		}
		require.NotNil(t, j.OccurrenceDate, "job %d", j.ID)
		p, ok := plans[*j.RecurringPlanID]
		require.True(t, ok, "job %d references a missing plan", j.ID)
		rule := planRule(p, loc)

		y, m, d := j.OccurrenceDate.Date()
		occ := time.Date(y, m, d, 0, 0, 0, 0, loc)
		matches := rule.Between(occ, occ.AddDate(0, 0, 1))
		require.Len(t, matches, 1, "job %d: occurrence %s is not a visit of plan %d", j.ID, occ.Format(time.DateOnly), p.ID)
		assert.True(t, rule.At(matches[0].Index).Equal(occ))

		assert.True(t, occ.Before(today(t)), "only past visits are booked")
		assert.Equal(t, occ.Format(time.DateOnly), j.ScheduledAt.In(loc).Format(time.DateOnly))
		assert.Equal(t, "completed", j.Status)
		assert.NotNil(t, j.CompletedAt)
		assert.Equal(t, p.CustomerID, j.CustomerID)

		key := visit{p.ID, occ.Format(time.DateOnly)}
		assert.False(t, seen[key], "two jobs for plan %d on %s", p.ID, key.date)
		seen[key] = true
		linkedPerPlan[p.ID]++
	}
	for _, p := range ds.RecurringPlans {
		assert.Positive(t, linkedPerPlan[p.ID], "plan %d has booked past visits", p.ID)
		// All visits from today on stay unbooked, so the forecast has a recurring series.
		rule := planRule(p, loc)
		future := rule.Between(time.Date(todayY, todayM, todayD, 0, 0, 0, 0, loc), time.Date(todayY+1, 1, 1, 0, 0, 0, 0, loc))
		assert.NotEmpty(t, future, "plan %d has future visits", p.ID)
	}
}

func TestDemoRecurringPlansAreReproducible(t *testing.T) {
	assert.Equal(t, demo(t, 12, 5).RecurringPlans, demo(t, 12, 5).RecurringPlans)
}
