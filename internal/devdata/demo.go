package devdata

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/shopspring/decimal"
)

// Bounds for DemoOptions.Months.
const (
	MinDemoMonths = 1
	MaxDemoMonths = 36
)

// ErrInvalidMonths is returned when DemoOptions.Months is outside [MinDemoMonths, MaxDemoMonths].
var ErrInvalidMonths = errors.New("invalid demo months")

// DemoOptions controls the generated demo dataset.
type DemoOptions struct {
	Months int       // months of history, ending with the month of Today
	Seed   uint64    // same seed, same data
	Today  time.Time // local midnight of the current day in the business timezone
}

// Dataset is a complete set of rows with IDs already assigned, ready to insert in field order.
type Dataset struct {
	Staff         []Staff
	Services      []Service
	Customers     []Customer
	Locations     []Location
	Tags          []Tag
	CustomerTags  []CustomerTag
	LocationTags  []LocationTag
	Jobs          []Job
	Documents     []Document
	DocumentItems []DocumentItem
	Payments      []Payment
}

// Staff is a row of the staff table.
type Staff struct {
	ID   int64
	Name string
}

// Service is a row of the services table.
type Service struct {
	ID           int64
	Name         string
	Price        decimal.Decimal
	IsRecurring  bool
	IntervalDays *int32
}

// Customer is a row of the customers table.
type Customer struct {
	ID        int64
	Name      string
	Email     string
	Status    string
	Source    *string
	CreatedAt time.Time
}

// Location is a row of the locations table.
type Location struct {
	ID         int64
	CustomerID int64
	Address    string
	City       string
}

// Tag is a row of the tags table.
type Tag struct {
	ID    int64
	Name  string
	Scope string
}

// CustomerTag is a row of the customer_tags table.
type CustomerTag struct{ CustomerID, TagID int64 }

// LocationTag is a row of the location_tags table.
type LocationTag struct{ LocationID, TagID int64 }

// Job is a row of the jobs table.
type Job struct {
	ID           int64
	CustomerID   int64
	LocationID   int64
	ServiceID    *int64
	StaffID      *int64
	Status       string
	BookedOnline bool
	Total        decimal.Decimal
	ScheduledAt  time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
}

// Document is a row of the documents table (an invoice or an estimate).
type Document struct {
	ID               int64
	DocType          string
	Status           string
	Number           string
	CustomerID       int64
	JobID            *int64
	SourceDocumentID *int64
	IssuedAt         time.Time
	DueDate          *time.Time
	ExpiresAt        *time.Time
	Subtotal         decimal.Decimal
	Tax              decimal.Decimal
	Total            decimal.Decimal
	AmountPaid       decimal.Decimal
}

// DocumentItem is a row of the document_items table.
type DocumentItem struct {
	ID          int64
	DocumentID  int64
	ServiceID   *int64
	Description string
	Quantity    decimal.Decimal
	UnitPrice   decimal.Decimal
}

// Payment is a row of the payments table.
type Payment struct {
	ID         int64
	DocumentID int64
	Method     string
	Amount     decimal.Decimal
	ReceivedAt time.Time
}

type demoService struct {
	name         string
	priceCents   int64
	intervalDays int32 // 0 = not recurring
}

var (
	demoStaff    = []string{"Thọ Nguyễn Quang", "Lan Trần", "Minh Phạm", "Schedule 4"}
	demoServices = []demoService{
		{"Blank Service", 14500, 0},
		{"Initial Service", 9500, 0},
		{"Bi-Monthly Service", 6500, 60},
		{"Every 21 Days", 6000, 21},
		{"Deep Clean", 22000, 0},
		{"Call Back Service", 0, 0},
	}
	demoLocationTags = []string{"1233", "a456", "north", "south"}
	demoCustomerTags = []string{"vip", "late-payer", "commercial"}
	demoCities       = []string{"Ho Chi Minh City", "Thu Duc", "Bien Hoa", "Di An"}
)

type weighted[T any] struct {
	value  T
	weight int
}

func pick[T any](r *rand.Rand, options []weighted[T]) T {
	total := 0
	for _, o := range options {
		total += o.weight
	}
	n := r.IntN(total)
	for _, o := range options {
		if n < o.weight {
			return o.value
		}
		n -= o.weight
	}
	return options[len(options)-1].value
}

func strPtr(s string) *string { return &s }

var (
	sourceWeights = []weighted[*string]{
		{nil, 40}, {strPtr("google"), 18}, {strPtr("facebook"), 12}, {strPtr("referral"), 12},
		{strPtr("yelp"), 8}, {strPtr("angieslist"), 5}, {strPtr("craigslist"), 5},
	}
	customerStatusWeights = []weighted[string]{{"active", 60}, {"lead", 30}, {"inactive", 10}}
	pastJobStatusWeights  = []weighted[string]{
		{"completed", 70}, {"incomplete", 14}, {"canceled", 8}, {"terminated", 4}, {"reschedule", 4},
	}
	futureJobStatusWeights = []weighted[string]{{"incomplete", 80}, {"reschedule", 10}, {"workpool", 10}}
	invoiceStatusWeights   = []weighted[string]{
		{"paid", 55}, {"sent", 15}, {"partial", 8}, {"partial_paid", 5},
		{"draft", 8}, {"void", 3}, {"write_off", 3}, {"failed", 3},
	}
	estimateStatusWeights = []weighted[string]{
		{"draft", 20}, {"pending", 25}, {"won", 20}, {"invoiced", 20}, {"lost", 15},
	}
	methodWeights = []weighted[string]{{"check", 45}, {"stripe", 30}, {"card", 15}, {"cash", 10}}
)

// generator assigns IDs as it creates rows, so references between rows are known up front.
type generator struct {
	r           *rand.Rand
	ds          Dataset
	start       time.Time // first instant of the window (local midnight on the 1st)
	now         time.Time // last instant data may be dated at: end of Today
	horizon     time.Time // last instant future jobs may be scheduled at
	invoiceSeq  int
	estimateSeq int
}

// Demo generates a realistic, reproducible dataset covering opts.Months months up to opts.Today.
func Demo(opts DemoOptions) (*Dataset, error) {
	if opts.Months < MinDemoMonths || opts.Months > MaxDemoMonths {
		return nil, fmt.Errorf("%w: %d, must be between %d and %d", ErrInvalidMonths, opts.Months, MinDemoMonths, MaxDemoMonths)
	}
	loc := opts.Today.Location()
	y, m, d := opts.Today.Date()

	g := &generator{
		// Demo data only needs to be reproducible, not unpredictable.
		r:       rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15)), //nolint:gosec // G404: must be seeded so --seed reproduces the same demo data; not used for security
		start:   time.Date(y, m-time.Month(opts.Months-1), 1, 0, 0, 0, 0, loc),
		now:     time.Date(y, m, d+1, 0, 0, 0, 0, loc).Add(-time.Minute),
		horizon: time.Date(y, m+1, d, 18, 0, 0, 0, loc),
	}
	g.staffAndServices()
	g.tags()
	for month := g.start; month.Before(g.now); month = month.AddDate(0, 1, 0) {
		g.customersFor(month)
	}
	return &g.ds, nil
}

func (g *generator) staffAndServices() {
	for i, name := range demoStaff {
		g.ds.Staff = append(g.ds.Staff, Staff{ID: int64(i + 1), Name: name})
	}
	for i, s := range demoServices {
		svc := Service{ID: int64(i + 1), Name: s.name, Price: decimal.New(s.priceCents, -2)}
		if s.intervalDays > 0 {
			days := s.intervalDays
			svc.IsRecurring, svc.IntervalDays = true, &days
		}
		g.ds.Services = append(g.ds.Services, svc)
	}
}

func (g *generator) tags() {
	for _, name := range demoLocationTags {
		g.ds.Tags = append(g.ds.Tags, Tag{ID: int64(len(g.ds.Tags) + 1), Name: name, Scope: "location"})
	}
	for _, name := range demoCustomerTags {
		g.ds.Tags = append(g.ds.Tags, Tag{ID: int64(len(g.ds.Tags) + 1), Name: name, Scope: "customer"})
	}
}

// between returns a random instant in [from, to], during working hours when the span allows it.
func (g *generator) between(from, to time.Time) time.Time {
	if !to.After(from) {
		return from
	}
	t := from.Add(time.Duration(g.r.Int64N(int64(to.Sub(from)))))
	if h := t.Hour(); h < 8 || h > 18 {
		shifted := time.Date(t.Year(), t.Month(), t.Day(), 8+g.r.IntN(10), g.r.IntN(60), 0, 0, t.Location())
		if !shifted.Before(from) && !shifted.After(to) {
			return shifted
		}
	}
	return t
}

func (g *generator) customersFor(month time.Time) {
	monthEnd := month.AddDate(0, 1, 0).Add(-time.Minute)
	if monthEnd.After(g.now) {
		monthEnd = g.now
	}
	count := 30 + g.r.IntN(21)
	for range count {
		id := int64(len(g.ds.Customers) + 1)
		c := Customer{
			ID:        id,
			Name:      fmt.Sprintf("Customer %04d", id),
			Email:     fmt.Sprintf("customer%04d@example.test", id),
			Status:    pick(g.r, customerStatusWeights),
			Source:    pick(g.r, sourceWeights),
			CreatedAt: g.between(month, monthEnd),
		}
		g.ds.Customers = append(g.ds.Customers, c)

		loc := Location{
			ID:         int64(len(g.ds.Locations) + 1),
			CustomerID: id,
			Address:    fmt.Sprintf("%d Demo Street", 1+g.r.IntN(400)),
			City:       demoCities[g.r.IntN(len(demoCities))],
		}
		g.ds.Locations = append(g.ds.Locations, loc)

		if g.r.IntN(5) == 0 {
			tag := g.ds.Tags[len(demoLocationTags)+g.r.IntN(len(demoCustomerTags))]
			g.ds.CustomerTags = append(g.ds.CustomerTags, CustomerTag{CustomerID: id, TagID: tag.ID})
		}
		if g.r.IntN(5) == 0 {
			tag := g.ds.Tags[g.r.IntN(len(demoLocationTags))]
			g.ds.LocationTags = append(g.ds.LocationTags, LocationTag{LocationID: loc.ID, TagID: tag.ID})
		}

		if c.Status != "lead" {
			for range g.r.IntN(4) {
				g.job(c, loc)
			}
		}
		for range g.r.IntN(3) {
			g.estimate(c)
		}
	}
}

func (g *generator) job(c Customer, loc Location) {
	svc := g.ds.Services[g.r.IntN(len(g.ds.Services))]
	scheduled := g.between(c.CreatedAt, g.horizon)

	status := pick(g.r, pastJobStatusWeights)
	if scheduled.After(g.now) {
		status = pick(g.r, futureJobStatusWeights)
	}

	// Price varies ±20% around the list price.
	base := svc.Price.Shift(2).IntPart()
	cents := base * int64(80+g.r.IntN(41)) / 100
	if base == 0 {
		cents = 0
	}

	j := Job{
		ID:           int64(len(g.ds.Jobs) + 1),
		CustomerID:   c.ID,
		LocationID:   loc.ID,
		ServiceID:    &svc.ID,
		Status:       status,
		BookedOnline: g.r.IntN(100) < 15,
		Total:        decimal.New(cents, -2),
		ScheduledAt:  scheduled,
		CreatedAt:    g.between(c.CreatedAt, minTime(scheduled, g.now)),
	}
	if g.r.IntN(10) > 0 {
		staffID := g.ds.Staff[g.r.IntN(len(g.ds.Staff))].ID
		j.StaffID = &staffID
	}
	if status == "completed" {
		done := scheduled.Add(time.Duration(1+g.r.IntN(3)) * time.Hour)
		j.CompletedAt = &done
	}
	g.ds.Jobs = append(g.ds.Jobs, j)

	if status == "completed" && cents > 0 && g.r.IntN(10) > 0 {
		issued := g.between(*j.CompletedAt, minTime(j.CompletedAt.Add(72*time.Hour), g.now))
		g.invoice(c.ID, &j.ID, nil, svc, cents, issued)
	}
}

func (g *generator) estimate(c Customer) {
	svc := g.ds.Services[g.r.IntN(len(g.ds.Services)-1)] // never the free Call Back Service
	cents := svc.Price.Shift(2).IntPart() * int64(90+g.r.IntN(61)) / 100
	issued := g.between(c.CreatedAt, g.now)
	status := pick(g.r, estimateStatusWeights)
	expires := calendarDate(issued).AddDate(0, 0, 30)

	g.estimateSeq++
	e := Document{
		ID:         int64(len(g.ds.Documents) + 1),
		DocType:    "estimate",
		Status:     status,
		Number:     fmt.Sprintf("EST-%05d", g.estimateSeq),
		CustomerID: c.ID,
		IssuedAt:   issued,
		ExpiresAt:  &expires,
		Subtotal:   decimal.New(cents, -2),
		Tax:        decimal.Zero,
		Total:      decimal.New(cents, -2),
		AmountPaid: decimal.Zero,
	}
	g.ds.Documents = append(g.ds.Documents, e)
	g.item(e.ID, svc, cents)

	if status == "invoiced" {
		// The accepted estimate becomes a completed job, invoiced from that job, so its
		// revenue is attributed to the staff member who did the work.
		converted := g.between(issued, minTime(issued.Add(10*24*time.Hour), g.now))
		staffID := g.ds.Staff[g.r.IntN(len(g.ds.Staff))].ID
		done := converted
		j := Job{
			ID:          int64(len(g.ds.Jobs) + 1),
			CustomerID:  c.ID,
			LocationID:  g.ds.Locations[c.ID-1].ID, // every customer gets exactly one location, created alongside it
			ServiceID:   &svc.ID,
			StaffID:     &staffID,
			Status:      "completed",
			Total:       decimal.New(cents, -2),
			ScheduledAt: converted,
			CompletedAt: &done,
			CreatedAt:   issued,
		}
		g.ds.Jobs = append(g.ds.Jobs, j)
		g.invoice(c.ID, &j.ID, &e.ID, svc, cents, converted)
	}
}

func (g *generator) invoice(customerID int64, jobID, sourceID *int64, svc Service, cents int64, issued time.Time) {
	status := pick(g.r, invoiceStatusWeights)
	if sourceID != nil && (status == "draft" || status == "void") {
		status = "sent" // an estimate was converted, so the invoice went out
	}
	var paid int64
	switch status {
	case "paid":
		paid = cents
	case "partial", "partial_paid":
		paid = cents * int64(30+g.r.IntN(41)) / 100
	}
	due := calendarDate(issued).AddDate(0, 0, 14)

	g.invoiceSeq++
	inv := Document{
		ID:               int64(len(g.ds.Documents) + 1),
		DocType:          "invoice",
		Status:           status,
		Number:           fmt.Sprintf("INV-%05d", g.invoiceSeq),
		CustomerID:       customerID,
		JobID:            jobID,
		SourceDocumentID: sourceID,
		IssuedAt:         issued,
		DueDate:          &due,
		Subtotal:         decimal.New(cents, -2),
		Tax:              decimal.Zero,
		Total:            decimal.New(cents, -2),
		AmountPaid:       decimal.New(paid, -2),
	}
	g.ds.Documents = append(g.ds.Documents, inv)
	g.item(inv.ID, svc, cents)

	if paid > 0 {
		// Paid invoices are sometimes settled in two payments.
		parts := []int64{paid}
		if status == "paid" && paid >= 200 && g.r.IntN(4) == 0 {
			first := paid / 2
			parts = []int64{first, paid - first}
		}
		received := issued
		for _, amount := range parts {
			received = g.between(received, minTime(received.Add(20*24*time.Hour), g.now))
			g.ds.Payments = append(g.ds.Payments, Payment{
				ID:         int64(len(g.ds.Payments) + 1),
				DocumentID: inv.ID,
				Method:     pick(g.r, methodWeights),
				Amount:     decimal.New(amount, -2),
				ReceivedAt: received,
			})
		}
	}
}

func (g *generator) item(documentID int64, svc Service, cents int64) {
	g.ds.DocumentItems = append(g.ds.DocumentItems, DocumentItem{
		ID:          int64(len(g.ds.DocumentItems) + 1),
		DocumentID:  documentID,
		ServiceID:   &svc.ID,
		Description: svc.Name,
		Quantity:    decimal.NewFromInt(1),
		UnitPrice:   decimal.New(cents, -2),
	})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// calendarDate returns t's local calendar day as UTC midnight, the form used for date columns,
// so the stored date can't shift when the value is converted between zones.
func calendarDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
