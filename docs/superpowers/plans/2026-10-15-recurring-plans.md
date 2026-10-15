# Recurring Service Plans Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Customers' recurring service plans, with visits, invoices and estimates projected on read, plus a recurring series in the Sales Forecast.

**Architecture:** A new `recurring_plans` table and a visit link on `jobs` (migration 000007). A pure engine, `internal/recurrence`, produces visit dates, and pure builders in `internal/model` turn visits into money. `RecurringService` (CRUD, transitions, projections) sits on a sqlc-backed `RecurringRepository`. A new `RecurringHandler` exposes `/api/v1/recurring-plans`. `DashboardService.SalesForecast` adds a recurring series through a `RecurringForecaster` interface, inside one snapshot transaction.

**Tech Stack:** Go 1.26, Gin + validator/v10, pgx/v5 + pgxpool, sqlc, golang-migrate, shopspring/decimal, testify + mockery.

**Spec:** `docs/superpowers/specs/2026-10-15-recurring-plans-design.md` (commit `cda5440`)

**Plan file on approval:** saved to `docs/superpowers/plans/2026-10-15-recurring-plans.md` and committed.

## Global Constraints
- Layering: handler → service → repository; services never import Gin; no SQL outside `internal/repository`.
- Parameterized sqlc queries only; batch with `= ANY(@ids)`; no N+1.
- Every repository call: `ctx, cancel := context.WithTimeout(ctx, r.timeout)`, and it reads its tx from the context.
- Wrap errors as `fmt.Errorf("<what>: %w", err)`. Map errors to HTTP **only** in `internal/handler/errors.go`.
- Money: `numeric` ↔ `decimal.Decimal`; API money via `dto.Money` (2-decimal strings); rounding `decimal.Round(2)`.
- Dates: business-TZ local midnight built with `time.Date`, never `Add(24h)`. "Today" = `daterange.Resolver.Today()`.
- Window caps: 24 months per plan, 12 months per customer. Plan list `limit` default 50, max 200.
- Tests: unit tests in `tests/unit/<pkg>/` (table-driven, testify, mockery); integration tests in `tests/integration/<feature>/` tagged `//go:build integration`, using `testdb.StartMigrated`.
- Every task ends with `go tool task check` green. Tasks that touch the DB also need `go tool task test:integration` green. No `//nolint` added to silence findings.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus
1. **An old anchor on the 31st.** Plan `month×1`, start 2025-01-31, today 2026-10-15 → first projected visit is 2026-10-31 with `Index` 21, and the next is 2026-11-30 (Task 2, Task 6).
2. **`to` before `from`** in any projection query → 400 `ErrInvalidQuery`, not an empty list or a 500 (Task 8).
3. **An active plan whose `end_date` is already past** → occurrences/invoices `[]`, the estimate has `visit_count` 0 and totals "0.00", and it adds nothing to the forecast (Task 6, Task 9).
4. **Over-precise decimal strings** (`unit_price "10.005"`, `quantity "1.234"`, `tax_rate "0.12345"`, `"abc"`, `"-1"`) → 400 `ErrInvalidBody` naming the field, never silently rounded and never a DB numeric error (Task 7).
5. **A customer route for a customer that doesn't exist** → 404 `ErrCustomerNotFound`. An existing customer with no plans → 200 `[]` (Task 6, Task 8).

---

### Task 1: Migration 000007 + schema constraints

**Files:**
- Create: `migrations/000007_recurring_plans.up.sql`, `migrations/000007_recurring_plans.down.sql`
- Modify: `tests/integration/schema/schema_test.go` (seed + constraint cases)

**Interfaces:** Produces the tables and columns exactly as in spec §3 (`recurring_plans`, `locations_id_customer_id_key`, `jobs.recurring_plan_id`, `jobs.occurrence_date`, `UNIQUE (recurring_plan_id, occurrence_date)`). The down migration drops them in reverse order (jobs constraint/columns → `recurring_plans` → the `locations` unique).

- [ ] **Step 1: Write failing constraint tests** `TestRecurringPlanConstraints` (table-driven, each case inside a rolled-back tx, following the existing `seedData` pattern). Insert a valid plan, then expect:
  - location of another customer → `23503`
  - `interval_count = 0` → `23514`; `end_date < start_date` → `23514`; `tax_rate = 1.5` → `23514`; `interval_unit = 'year'` → `23514`
  - job with `recurring_plan_id` set and `occurrence_date` NULL → `23514`
  - two jobs with the same `(recurring_plan_id, occurrence_date)` → `23505`
  - valid plan + linked job → no error
- [ ] **Step 2: Run** `go test -tags=integration ./tests/integration/schema/ -run 'TestRecurringPlanConstraints|TestMigrationsUpDownUp' -v`. Expected: FAIL (relation "recurring_plans" does not exist).
- [ ] **Step 3: Write the up/down SQL** per spec §3, with a top comment in the style of `000001` explaining the projected-on-read model.
- [ ] **Step 4: Run** the same command. Expected: PASS, including `TestMigrationsUpDownUp` (down applies cleanly).
- [ ] **Step 5: Run `sqlc generate`.** Expected: `internal/repository/db/models.go` gains `RecurringPlan` and `Job.RecurringPlanID`/`OccurrenceDate`, and `go build ./...` passes.
- [ ] **Step 6: Commit** `feat(db): add recurring_plans and job visit link (000007)`.

### Task 2: Recurrence engine

**Files:**
- Create: `internal/recurrence/recurrence.go`
- Test: `tests/unit/recurrence/recurrence_test.go`

**Interfaces — Produces:**
```go
type Unit string
const (Day Unit = "day"; Week Unit = "week"; Month Unit = "month")
type Rule struct { Unit Unit; Count int; Start time.Time; End *time.Time }
type Occurrence struct { Index int; Date time.Time }
var ErrInvalidRule = errors.New("invalid recurrence rule")
func (r Rule) Validate() error
func (r Rule) At(k int) time.Time
func (r Rule) Between(from, to time.Time) []Occurrence   // half-open; never nil (empty slice)
func DateKey(t time.Time) string                        // t.Format("2006-01-02")
```

- [ ] **Step 1: Write failing tests.**
  - `TestAt` table: `{day,3,2026-01-01,k=2} → 2026-01-07`; `{week,2,2026-01-05,k=1} → 2026-01-19`; `{month,1,2026-01-31}` k=1..3 → `2026-02-28, 2026-03-31, 2026-04-30`; `{month,1,2028-01-31,k=1} → 2028-02-29`; `{month,12,2028-02-29,k=1} → 2029-02-28`, k=4 → `2032-02-29`; k=0 → Start.
  - `TestBetween` table: window entirely before Start → `[]`; after End → `[]`; visit exactly on End included; visit exactly on `to` excluded; `from` exactly on a visit included; `from >= to` → `[]`; **Review Focus 1:** `{month,1,2025-01-31}`, window `[2026-10-15, 2026-12-01)` → `[{21, 2026-10-31}, {22, 2026-11-30}]`.
  - `TestBetweenDST`: `America/New_York`, `{day,1,2026-03-07}`, window `[2026-03-07, 2026-03-10)` → 3 visits, each `Hour()==0`; the same for 2026-10-31…11-03.
  - `TestBetweenProperties`: for every `TestBetween` case, the results are strictly increasing, inside `[from,to)` and `<= End`, and `o.Date.Equal(rule.At(o.Index))`.
  - `TestValidate`: unit `"year"`, Count 0, End before Start → `errors.Is(err, ErrInvalidRule)`; a valid rule → nil.
- [ ] **Step 2: Run** `go test ./tests/unit/recurrence/ -v`. Expected: FAIL (package not found).
- [ ] **Step 3: Implement** per spec §4. `Between` first-index estimate: day/week `k0 = max(0, ceilDiv(daysBetween(Start, from), step))`, where `daysBetween` uses calendar fields (`time.Date(...UTC)` difference ÷ 24h). Month: `k0 = max(0, monthsBetween(Start, from)/Count - 1)`. Then walk forward.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(recurrence): add cadence engine`.

### Task 3: Domain types, errors and money builders

**Files:**
- Create: `internal/model/recurring.go`
- Test: `tests/unit/model/recurring_test.go` (new directory)

**Interfaces — Produces** (in addition to the `RecurringPlan`, `ProjectedLine`, `ProjectedInvoice`, `ProjectedEstimate` fields from spec §5):
```go
const (PlanStatusActive = "active"; PlanStatusPaused = "paused"; PlanStatusCanceled = "canceled")
type PlanAction string
const (PlanPause PlanAction = "pause"; PlanResume PlanAction = "resume"; PlanCancel PlanAction = "cancel")
var (ErrPlanNotFound, ErrCustomerNotFound, ErrPlanCanceled, ErrInvalidTransition,
     ErrCadenceLocked, ErrInvalidReference, ErrWindowTooLarge error) // errors.New with short lowercase messages
type Optional[T any] struct { Set bool; Value T }
type NewRecurringPlan struct { CustomerID, ServiceID int64; LocationID, StaffID *int64; Rule recurrence.Rule
    Quantity decimal.Decimal; UnitPrice *decimal.Decimal /* nil = service price */; TaxRate decimal.Decimal; PaymentTermsDays int }
type RecurringPlanPatch struct { LocationID, StaffID Optional[*int64]; EndDate Optional[*time.Time]
    IntervalUnit Optional[recurrence.Unit]; IntervalCount Optional[int]; StartDate Optional[time.Time]
    Quantity, UnitPrice, TaxRate Optional[decimal.Decimal]; PaymentTermsDays Optional[int] }
func (p RecurringPlanPatch) ChangesCadence() bool
func (p RecurringPlanPatch) Apply(plan RecurringPlan) RecurringPlan
type PlanListQuery struct { CustomerID *int64; Status *string; After int64; Limit int }
type PlanPage struct { Items []RecurringPlan; NextCursor *int64 }
type Window struct { From, To time.Time } // half-open
type PlanOccurrence struct { PlanID int64; recurrence.Occurrence; JobID *int64 }
type LinkedVisits map[int64]map[string]int64 // planID → DateKey → jobID
func NextPlanStatus(current string, a PlanAction) (string, error)
func ProjectInvoice(p RecurringPlan, o recurrence.Occurrence) ProjectedInvoice
func ProjectEstimate(p RecurringPlan, occs []recurrence.Occurrence, w Window) ProjectedEstimate
```

- [ ] **Step 1: Write failing tests.**
  - `TestProjectInvoice` table: qty 1.5 × 33.33 → Amount `50.00` (from 49.995); unit 100, rate 0.0825 → Tax `8.25`, Total `108.25`; Subtotal 0.06 × rate 0.0825 = 0.00495 → Tax `0.00`; Subtotal 10.10 × 0.05 = 0.505 → Tax `0.51` (half away from zero); rate 0 → Tax `0`; visit 2026-01-30 + 5 terms → DueOn 2026-02-04; `Ref == "PRJ-7-3"` for plan 7, index 3; `Line.Description == ServiceName`.
  - `TestProjectEstimateEqualsSumOfInvoices`: for 1, 3 and 52 visits at unit 33.33, rate 0.0825, Subtotal/Tax/Total equal the sums of the `ProjectInvoice` values; `VisitCount`; `Line.Quantity = n × plan.Quantity`; zero visits → all `0`.
  - `TestNextPlanStatus` table: active+pause→paused; paused+resume→active; active/paused+cancel→canceled; paused+pause→paused (idempotent); active+resume→active; canceled+resume / canceled+pause → `ErrInvalidTransition`; canceled+cancel → canceled.
  - `TestPatchChangesCadence` / `TestPatchApply`: only fields with `Set` change; setting `LocationID` to a nil value clears it.
- [ ] **Step 2: Run** `go test ./tests/unit/model/ -v`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** in `internal/model/recurring.go`. `Ref` = `fmt.Sprintf("PRJ-%d-%d", planID, index)`. DueOn = `time.Date(y, m, d+PaymentTermsDays, 0,0,0,0, loc)`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(model): add recurring plan types and projection builders`.

### Task 4: Repository

**Files:**
- Create: `internal/repository/queries/recurring.sql`, `internal/repository/recurring.go`, `internal/repository/tx.go`
- Modify: `internal/repository/dashboard.go` (move `WithTx` body and `queries` into `tx.go` as package helpers; `DashboardRepository.WithTx` delegates)
- Test: `tests/integration/recurring/repository_test.go`, `tests/integration/recurring/setup_test.go`

**Interfaces:**
- Consumes: Task 1 schema; Task 3 types.
- Produces:
```go
func withTx(ctx context.Context, conn Conn, opts model.TxOptions, fn func(ctx context.Context) error) error // shared txKey
func queriesFor(ctx context.Context, q *db.Queries) *db.Queries
func NewRecurringRepository(conn Conn, timeout time.Duration, tz *time.Location) *RecurringRepository
// methods (exactly the service interface in Task 5):
WithTx, ServicePrice(ctx, serviceID int64) (decimal.Decimal, bool /*active*/, error),
CustomerExists(ctx, id int64) (bool, error), CreatePlan(ctx, model.NewRecurringPlan /*UnitPrice set*/) (model.RecurringPlan, error),
GetPlan, GetPlanForUpdate(ctx, id int64) (model.RecurringPlan, error), ListPlans(ctx, model.PlanListQuery) ([]model.RecurringPlan, error),
UpdatePlan(ctx, model.RecurringPlan) (model.RecurringPlan, error), SetPlanStatus(ctx, id int64, status string) (model.RecurringPlan, error),
PlanHasLinkedJobs(ctx, id int64) (bool, error), ActivePlansForCustomer(ctx, customerID int64, w model.Window) ([]model.RecurringPlan, error),
ActivePlansOverlapping(ctx, w model.Window) ([]model.RecurringPlan, error), LinkedOccurrences(ctx, planIDs []int64, w model.Window) (model.LinkedVisits, error)
```
- Mapping: `pgx.ErrNoRows` → `model.ErrPlanNotFound` (`ServicePrice` → `model.ErrInvalidReference`). A pg `23503` on insert/update → `model.ErrInvalidReference`. Dates: DB `time.Time` (UTC midnight) → `time.Date(y, m, d, 0,0,0,0, r.tz)`; writes pass `time.Date(y, m, d, 0,0,0,0, time.UTC)`.
- SQL: every plan read joins `services` for `service_name`. `ListPlans`: `WHERE id > @after AND (@customer_id::bigint IS NULL OR customer_id = @customer_id) AND (@status::text IS NULL OR status = @status) ORDER BY id LIMIT @lim`. `UpdatePlan` sets `updated_at = now()`. Overlap: `status='active' AND start_date < @to AND (end_date IS NULL OR end_date >= @from)`.

- [ ] **Step 1: Write failing integration tests** (`seedPlan(t, pool, opts)` helper in `setup_test.go`). Cases: create → get round-trip with every field (dates come back in the business TZ, `ServiceName` filled); `GetPlan(999)` → `ErrPlanNotFound`; a cross-customer location on create → `ErrInvalidReference`; `ListPlans` keyset over 5 plans with limit 2 → ids `[1,2]`, `[3,4]`, `[5]`, plus customer and status filters; `LinkedOccurrences` for 2 plans returns both jobs keyed by `DateKey`, and leaves out a job outside the window; `ActivePlansOverlapping` excludes paused, ended-before-window and starting-after-window plans; `GetPlanForUpdate` inside `WithTx` blocks a second `WithTx` `SetPlanStatus` until the first commits (2 goroutines, ordering asserted with channels).
- [ ] **Step 2: Run** `go test -tags=integration ./tests/integration/recurring/ -v`. Expected: FAIL (undefined `NewRecurringRepository`).
- [ ] **Step 3: Write `recurring.sql`, run `sqlc generate`, and implement `tx.go` + `recurring.go`.**
- [ ] **Step 4: Run** the same command plus `go test -tags=integration ./tests/integration/repository/` (WithTx refactor). Expected: PASS.
- [ ] **Step 5: Commit** `feat(repository): add recurring plan repository`.

### Task 5: Service — CRUD, PATCH lock, transitions

**Files:**
- Create: `internal/service/recurring.go`
- Modify: `.mockery.yml` (add `RecurringRepository`, `RecurringForecaster` under the service package, and `RecurringService` under the handler package); regenerate with `go tool task generate`
- Test: `tests/unit/service/recurring_test.go`

**Interfaces:**
- Consumes: Task 4 method set as `type RecurringRepository interface{...}`.
- Produces: `func NewRecurringService(repo RecurringRepository, dates *daterange.Resolver) *RecurringService` with
  `Create(ctx, model.NewRecurringPlan) (model.RecurringPlan, error)`, `Get(ctx, id int64)`, `List(ctx, model.PlanListQuery) (model.PlanPage, error)`, `Update(ctx, id int64, model.RecurringPlanPatch) (model.RecurringPlan, error)`, `Transition(ctx, id int64, model.PlanAction) (model.RecurringPlan, error)`.

- [ ] **Step 1: Write failing tests** (mockery `MockRecurringRepository`, fixed clock):
  - Create: `UnitPrice` nil → takes `ServicePrice`; service inactive → `ErrInvalidReference`; `Rule.Validate` failure → `ErrInvalidRule`, and the repo is never called.
  - List: fetches `Limit+1`; `NextCursor` = last returned id only when there are more; `Limit` 0 → 50; >200 → 200.
  - Update: runs inside `WithTx` (mock `WithTx` calls fn); `GetPlanForUpdate` first; cadence change + `PlanHasLinkedJobs=true` → `ErrCadenceLocked`, `UpdatePlan` not called; cadence change + no jobs → updated; a non-cadence change doesn't call `PlanHasLinkedJobs`; patched rule invalid (end < start) → `ErrInvalidRule`; canceled plan → `ErrPlanCanceled`.
  - Transition: uses `model.NextPlanStatus`; same status → returns the plan without `SetPlanStatus`; invalid → `ErrInvalidTransition`.
  - Repo errors are wrapped (`errors.Is` still finds the sentinel and the message gets a prefix).
- [ ] **Step 2: Run** `go test ./tests/unit/service/ -run Recurring -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(service): recurring plan CRUD and transitions`.

### Task 6: Service — projections

**Files:**
- Modify: `internal/service/recurring.go`
- Test: `tests/unit/service/recurring_projection_test.go`

**Interfaces — Produces:**
```go
const MaxPlanWindowMonths, MaxCustomerWindowMonths = 24, 12
func (s *RecurringService) Occurrences(ctx, id int64, w model.Window) ([]model.PlanOccurrence, error)
func (s *RecurringService) ProjectedInvoices(ctx, id int64, w model.Window) ([]model.ProjectedInvoice, error)
func (s *RecurringService) ProjectedEstimate(ctx, id int64, w model.Window) (model.ProjectedEstimate, error)
func (s *RecurringService) CustomerOccurrences(ctx, customerID int64, w model.Window) ([]model.PlanOccurrence, error)
func (s *RecurringService) MonthlyRecurring(ctx, w model.Window) ([]model.MonthlyAmount, error) // satisfies RecurringForecaster
```
Rules from spec §5. The window is clamped to `From = max(w.From, today)`. The cap is checked on the **requested** window (`To > From.AddDate(0, cap, 0)` → `ErrWindowTooLarge`). Months for `MonthlyRecurring` = `time.Date(y, m, 1, 0,0,0,0, loc)` of each visit.

- [ ] **Step 1: Write failing tests** (fixed clock 2026-10-15 10:00 Asia/Ho_Chi_Minh):
  - Occurrences: a linked visit has `JobID`; paused → `[]`; window before today clamped; 25-month window → `ErrWindowTooLarge`; **Review Focus 1** anchor 2025-01-31 → first `{21, 2026-10-31}`; **Review Focus 3** active plan ended 2026-09-30 → `[]`.
  - ProjectedInvoices: linked visit removed; canceled → `[]`.
  - ProjectedEstimate: linked visit **kept**; paused allowed; canceled → `ErrPlanCanceled`; **Review Focus 3** → VisitCount 0, totals 0.
  - CustomerOccurrences: **Review Focus 5** `CustomerExists=false` → `ErrCustomerNotFound`; no plans → `[]`; 2 plans merged by date then plan ID; a 13-month window → `ErrWindowTooLarge`; `LinkedOccurrences` called once with both IDs (`.Once()`).
  - MonthlyRecurring: Subtotal (no tax) summed per month; linked visit excluded; months before today absent; `LinkedOccurrences` called once for 3 plans.
- [ ] **Step 2: Run** `go test ./tests/unit/service/ -run 'Projected|Occurrences|MonthlyRecurring' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat(service): recurring projections`.

### Task 7: Handler — CRUD and transition endpoints

**Files:**
- Create: `internal/handler/recurring.go`, `internal/handler/recurring_body.go` (decimal/date parsing), `internal/dto/recurring.go`
- Modify: `internal/handler/errors.go` (add `ErrInvalidBody`; `notFoundErrors`, `conflictErrors`; new 400 entries), `internal/handler/router.go` (`RouterDeps.Recurring *RecurringHandler`; routes registered only when non-nil, so existing tests keep working), `cmd/api/main.go` (wiring)
- Test: `tests/unit/handler/recurring_test.go`, `tests/integration/recurring/api_crud_test.go`

**Interfaces:**
- Consumes: Task 5/6 method set as `handler.RecurringService`.
- Produces: `func NewRecurringHandler(svc RecurringService, dates *daterange.Resolver) *RecurringHandler`; DTOs `CreateRecurringPlanRequest` (spec §6 tags), `UpdateRecurringPlanRequest` (fields as `dto.Optional[T]` with `UnmarshalJSON` that sets `Set`; `customer_id`/`service_id`/`status` rejected), `RecurringPlanResponse`, `RecurringPlanPage{Items, NextCursor *int64 \`json:"next_cursor"\`}`.
- Decimal rules: `quantity` > 0 with ≤2 places, `unit_price` ≥ 0 with ≤2 places, `tax_rate` 0..1 with ≤4 places. Otherwise `ErrInvalidBody: <field>: <reason>`. Binding errors are also wrapped as `ErrInvalidBody` (field names in the message).

- [ ] **Step 1: Write failing handler unit tests** (mock service): create 201 body shape; **Review Focus 4** table: `unit_price "10.005"`, `quantity "1.234"`, `tax_rate "0.12345"`, `"abc"`, `"-1"`, `interval_unit "year"`, `start_date "2026-13-01"` → 400 with the field name in `error`; PATCH with `customer_id` → 400; PATCH `{"location_id": null}` → patch has `LocationID.Set && Value == nil`; list `?limit=0&after=x` → 400; unknown query key → 400; error mapping: `ErrPlanNotFound` 404, `ErrCadenceLocked`/`ErrInvalidTransition`/`ErrPlanCanceled` 409, `ErrInvalidReference` 400.
- [ ] **Step 2: Run** `go test ./tests/unit/handler/ -run Recurring -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the handler, DTOs, error lists, routes and wiring.
- [ ] **Step 4: Write the integration test** `api_crud_test.go` (real router + DB): create → get → list with cursor paging → PATCH price → pause → resume → cancel → resume 409; PATCH cadence 409 after inserting a linked job; cross-customer location 400.
- [ ] **Step 5: Run** `go test ./tests/unit/handler/ -v` and `go test -tags=integration ./tests/integration/recurring/ -v`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(api): recurring plan CRUD endpoints`.

### Task 8: Handler — projection endpoints

**Files:**
- Modify: `internal/handler/recurring.go`, `internal/dto/recurring.go`, `internal/handler/router.go`
- Test: `tests/unit/handler/recurring_projection_test.go`, `tests/integration/recurring/api_projection_test.go`

**Interfaces — Produces:** `OccurrenceResponse`, `ProjectedInvoiceResponse`, `ProjectedEstimateResponse` (spec §6, `kind: "projected"`), each list wrapped with `range` (`dto.DateRange`, preset `"custom"`). `parseWindow(c, defaultMonths int) (model.Window, error)` handles `from`/`to` `YYYY-MM-DD`, `to` inclusive → `To = to + 1 day` in the business TZ, and the defaults.

- [ ] **Step 1: Write failing unit tests:** defaults (occurrences: `from` = 2026-10-15, inclusive `to` = 2027-01-15, so `Window.To` = 2027-01-16; estimate: inclusive `to` = 2027-10-15); `to=2026-10-31` → `Window.To` = 2026-11-01 local midnight; **Review Focus 2** `from=2026-11-01&to=2026-10-01` → 400; bad date → 400; unknown key → 400; **Review Focus 5** `ErrCustomerNotFound` → 404; response money as strings.
- [ ] **Step 2: Run** `go test ./tests/unit/handler/ -run 'Occurrence|Projected' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the four routes.
- [ ] **Step 4: Write the integration test:** a weekly plan + one linked job → occurrences show `job_id` on that date; projected invoices leave it out; the estimate counts it; the customer route merges 2 plans in date order; an unknown customer → 404.
- [ ] **Step 5: Run** the unit and integration commands. Expected: PASS.
- [ ] **Step 6: Commit** `feat(api): recurring projection endpoints`.

### Task 9: Sales Forecast recurring series

**Files:**
- Modify: `internal/model/dashboard.go` (`ForecastMonth{Month time.Time; Booked, Recurring decimal.Decimal}`, `ForecastSeries{Months []ForecastMonth; BookedTotal, RecurringTotal decimal.Decimal}`), `internal/service/dashboard.go`, `internal/handler/dashboard.go` (`DashboardService.SalesForecast` returns `model.ForecastSeries`), `internal/dto/dashboard.go` (`SalesForecastResponse`, `ForecastMonth{Month, Amount, Recurring}`), `cmd/api/main.go`, `tests/integration/dashboard/setup_test.go`; regenerate mocks
- Test: `tests/unit/service/sales_forecast_test.go`, `tests/unit/handler/dashboard_test.go` (forecast case), `tests/integration/dashboard/forecast_recurring_test.go`

**Interfaces:**
- Consumes: Task 6 `MonthlyRecurring`.
- Produces: `type RecurringForecaster interface { MonthlyRecurring(ctx context.Context, w model.Window) ([]model.MonthlyAmount, error) }` and `func (s *DashboardService) WithRecurring(f RecurringForecaster) *DashboardService`. A nil forecaster means recurring is all zeros, so the 24 existing `NewDashboardService(repo)` call sites stay unchanged.
- `SalesForecast` runs `s.repo.WithTx(ctx, model.TxOptions{ReadOnly: true, Snapshot: true}, ...)`, calls `MonthlyForecast` then `MonthlyRecurring(Window{r.Start, r.End})`, `fillMonths` on each, and zips them. Works because both repositories share `txKey` (Task 4).

- [ ] **Step 1: Write failing tests.** Service: booked unchanged; recurring zipped by month; totals; nil forecaster → zeros; forecaster error wrapped `"sales forecast widget: ..."`; `WithTx` called with `{ReadOnly: true, Snapshot: true}`. Handler: JSON has `total`, `recurring_total`, `months[].amount`, `months[].recurring`.
- [ ] **Step 2: Run** `go test ./tests/unit/service/ ./tests/unit/handler/ -run Forecast -v`. Expected: FAIL.
- [ ] **Step 3: Implement**, and update `setup_test.go` `at()` to `.WithRecurring(service.NewRecurringService(repository.NewRecurringRepository(e.pool, 5*time.Second, tz), dates))`.
- [ ] **Step 4: Write the integration test** `forecast_recurring_test.go` (clock 2026-10-12): a monthly plan at 100.00 from 2026-11-05 with tax 0.1 + a linked job on 2026-12-05 (total 100) → Nov `recurring` "100.00"; Dec `amount` includes the job and `recurring` "0.00" for that visit; Oct `recurring` "0.00"; recurring is tax excluded. **Review Focus 3** plan ended 2026-09-30 → adds nothing. Existing `TestSalesForecastCountsBookedJobs` and the `widgets_test.go` forecast case pass unchanged.
- [ ] **Step 5: Run** `go tool task test` and `go tool task test:integration`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(dashboard): recurring series in sales forecast`.

### Task 10: Demo seed data

**Files:**
- Modify: `internal/devdata/demo.go` (a `RecurringPlan` type, `Dataset.RecurringPlans`, `Job.RecurringPlanID`/`OccurrenceDate`), `internal/devdata/seed.go` (`tables`: `recurring_plans` before `jobs`; the jobs column list gets the two new columns)
- Test: `tests/unit/devdata/demo_test.go`, `tests/integration/devtools/devtools_test.go` (forecast now has `recurring_total` > 0)

- [ ] **Step 1: Write failing tests:** the demo dataset has ≥3 plans covering `week×1`, `week×2` and `month×1` anchored on the 31st; every plan's rule passes `Validate()`; every job with `RecurringPlanID` has an `OccurrenceDate` that equals `rule.At(k)` for some k; no two jobs share `(plan, date)`.
- [ ] **Step 2: Run** `go test ./tests/unit/devdata/ -v`. Expected: FAIL.
- [ ] **Step 3: Implement** with the generator's seeded `rand`, so output stays deterministic.
- [ ] **Step 4: Run** `go test ./tests/unit/devdata/ -v` and `go test -tags=integration ./tests/integration/devtools/ -v`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(devdata): seed recurring plans`.

### Task 11: Final verification

- [ ] **Step 1:** `go tool task check`. Expected: gofmt clean, golangci-lint 0 issues, unit tests PASS.
- [ ] **Step 2:** `go tool task test:integration`. Expected: PASS.
- [ ] **Step 3:** `go tool task seed -- --preset=demo --reset`, `go tool task run`, then `curl` `/api/v1/recurring-plans?limit=2`, `/api/v1/recurring-plans/1/projected-invoices` and `/api/v1/dashboard/sales-forecast`. Expected: 200s, `next_cursor` set, `recurring_total` > "0.00".
- [ ] **Step 4:** Report the results. Pushing to GitHub is left to the user.
