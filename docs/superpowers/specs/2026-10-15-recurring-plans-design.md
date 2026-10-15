# Recurring Service Plans: Design

Date: 2026-10-15 · Status: approved by sections, awaiting spec review

## 1. Purpose and scope

A customer signs up for a repeating service (for example lawn care every 2 weeks, or pool cleaning
monthly on the 15th). The system projects the plan's future visits, the invoices those visits would
produce, a plan-level estimate (its value over a period, used for quoting), and the expected recurring
revenue in the Sales Forecast.

Future visits and documents are **projected on read**. Nothing is stored ahead of time. A real `jobs`
row is created only when staff actually book a visit, and it is then linked to the visit it fulfils.

**In scope:** recurring plan CRUD and status API, projected occurrences, projected invoices, projected
estimate, and a recurring series in the Sales Forecast.

**Out of scope (possible follow-ups):** frontend in `web/`; turning a projection into a real
invoice/estimate/job; billing per cycle or flat fee (would be a `billing_mode` on the plan); RRULE cadences
("2nd Tuesday"); forecast caching.

## 2. Decisions

| # | Decision | Reason |
|---|---|---|
| D1 | Projections are computed on read, never stored | No scheduler, plan edits apply to the whole future at once, no orphaned rows |
| D2 | Cadence is every N `day`/`week`/`month` | Monthly-on-a-date is the common billing pattern and can't be written as N days; RRULE is more than needed |
| D3 | Real jobs link to a visit through `jobs.recurring_plan_id` + `occurrence_date` | An exact match that survives reschedules and skips; heuristics could match the wrong job |
| D4 | One projected invoice per visit; tax rate set per plan | Simplest and matches `documents.job_id`; the schema has no global tax setting |
| D5 | A pure Go engine (`internal/recurrence`) is the only source of visit dates | One implementation for every consumer, easy to unit-test; SQL `generate_series` would duplicate the rules |
| D6 | The estimate is the full plan value (booked visits included); invoices and the forecast skip booked visits | A quote shouldn't shrink as visits get booked; double counting is only a risk where real jobs are also summed |
| D7 | Every design section and every code change goes through plan mode first | The user's standing workflow rule (CLAUDE.md, Workflow step 2) |

## 3. Data model: migration `000007_recurring_plans`

New migration; applied migrations 000001–000006 are not edited.

### `locations`
Add `UNIQUE (id, customer_id)` as the target of a composite FK (the same technique as `tags (id, scope)`).

### New table `recurring_plans`

| column | type / constraint | notes |
|---|---|---|
| `id` | `bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY` | |
| `customer_id` | `bigint NOT NULL REFERENCES customers (id)` | |
| `location_id` | `bigint NULL`, `FOREIGN KEY (location_id, customer_id) REFERENCES locations (id, customer_id)` | a plan can't use another customer's location |
| `service_id` | `bigint NOT NULL REFERENCES services (id)` | |
| `staff_id` | `bigint NULL REFERENCES staff (id)` | default assignee shown on projected visits |
| `status` | `text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','canceled'))` | |
| `interval_unit` | `text NOT NULL CHECK (interval_unit IN ('day','week','month'))` | |
| `interval_count` | `int NOT NULL CHECK (interval_count > 0)` | (`week`, 2) = every 2 weeks |
| `start_date` | `date NOT NULL` | anchor and first visit; a local date in `BUSINESS_TZ` |
| `end_date` | `date NULL`, `CHECK (end_date >= start_date)` | inclusive; NULL = open-ended |
| `quantity` | `numeric(10,2) NOT NULL DEFAULT 1 CHECK (quantity > 0)` | |
| `unit_price` | `numeric(12,2) NOT NULL CHECK (unit_price >= 0)` | copied from `services.price` on create, then owned by the plan |
| `tax_rate` | `numeric(6,4) NOT NULL DEFAULT 0 CHECK (tax_rate BETWEEN 0 AND 1)` | 0.0825 = 8.25% |
| `payment_terms_days` | `int NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0)` | projected due date = visit date + N days |
| `created_at`, `updated_at` | `timestamptz NOT NULL DEFAULT now()` | |

Indexes: `customer_id`, `location_id`, `service_id`, `staff_id`, `(status, start_date)`.

### `jobs` additions
- `recurring_plan_id bigint NULL REFERENCES recurring_plans (id)`
- `occurrence_date date NULL`: the plan visit this job fulfils; unchanged when the job is rescheduled
- `CHECK ((recurring_plan_id IS NULL) = (occurrence_date IS NULL))`
- `UNIQUE (recurring_plan_id, occurrence_date)`: one job per visit; also serves as the FK index

The down migration drops all of the above in reverse order.

### Rules
- Only `active` plans are projected. `paused` projects nothing until resumed. `canceled` is final.
  Plans are never hard-deleted.
- Visits are projected only from **today (business TZ) onwards**. A past visit with no linked job is
  missed and is never projected again.
- A linked job covers its visit **whatever the job's status**. A canceled linked job stops that visit from
  being projected again.

## 4. Recurrence engine: `internal/recurrence`

Pure package: no I/O, Gin or database.

```go
type Unit string // "day" | "week" | "month"

type Rule struct {
    Unit  Unit
    Count int        // > 0
    Start time.Time  // first visit, local midnight in the business TZ
    End   *time.Time // inclusive; nil = open-ended
}

type Occurrence struct {
    Index int       // 0-based visit number counted from Start
    Date  time.Time // local midnight, same Location as Start
}

var ErrInvalidRule = errors.New("invalid recurrence rule")
```

- `Validate()`: an unknown unit, `Count <= 0`, or `End` before `Start` returns a wrapped `ErrInvalidRule`.
- `At(k)`: the k-th visit, always computed **from the anchor**, never by stepping from the previous visit:
  - day: `time.Date(y, m, d+k*Count, 0,0,0,0, loc)`; week: `d + 7*k*Count`;
  - month: target month `m + k*Count` (normalized by `time.Date`), day = `min(Start.Day(), lastDayOf(target))`.
    Jan 31 gives Feb 28/29, Mar 31, Apr 30, … with no drift.
- `Between(from, to)`: visits in the half-open window `[from, to)`, also capped by `End` (inclusive).
  Clamp the window to `[Start, End+1day)`. Estimate the first index (for months, step back one so
  clamping can't skip a visit). Walk `At(k)` until a date is `>= to`.
- All calendar math uses `time.Date` on year/month/day, never `Add(24h)`, so DST can't move a visit
  (the same rule as `daterange.Resolver`).
- The engine knows nothing about status, "today", or linked jobs. Those are service rules.

Integration: "today" comes from `daterange.Resolver.Today()`. sqlc returns `date` as UTC-midnight
`time.Time`; the repository converts it to business-TZ local midnight (`time.Date(y, m, d, 0,0,0,0, tz)`)
before building a `Rule`, and back when writing.

## 5. Projected invoices and estimates

### Domain types: `internal/model/recurring.go`

```go
type RecurringPlan struct {
    ID, CustomerID, ServiceID int64
    LocationID, StaffID       *int64
    ServiceName               string
    Status                    string // PlanStatusActive | PlanStatusPaused | PlanStatusCanceled
    Rule                      recurrence.Rule
    Quantity, UnitPrice       decimal.Decimal
    TaxRate                   decimal.Decimal
    PaymentTermsDays          int
    CreatedAt, UpdatedAt      time.Time
}

type ProjectedLine struct {
    Description         string
    Quantity, UnitPrice decimal.Decimal
    Amount              decimal.Decimal
}

type ProjectedInvoice struct {
    PlanID               int64
    Occurrence           recurrence.Occurrence
    Ref                  string    // "PRJ-<planID>-<index>"; never a real documents.number
    IssuedOn, DueOn      time.Time // visit date; visit date + PaymentTermsDays
    Line                 ProjectedLine
    Subtotal, Tax, Total decimal.Decimal
}

type ProjectedEstimate struct {
    PlanID               int64
    From, To             time.Time // half-open period actually covered
    VisitCount           int
    Line                 ProjectedLine // Quantity = VisitCount × plan.Quantity
    Subtotal, Tax, Total decimal.Decimal
}
```

### Money rules (pure functions in `internal/model`)
- `ProjectInvoice(plan, occurrence)`:
  `Amount = round(Quantity × UnitPrice, 2)` (the same formula as the generated column
  `document_items.amount`); `Subtotal = Amount`; `Tax = round(Subtotal × TaxRate, 2)`;
  `Total = Subtotal + Tax`. Rounding uses `decimal.Round(2)` (half away from zero), the same as Postgres
  `round(numeric, 2)`.
- `ProjectEstimate(plan, occurrences, from, to)`: Subtotal, Tax and Total are **sums of the per-visit
  rounded values**, so an estimate always equals the sum of the invoices for the same visits, to the cent.
- Amounts leave the API as strings through the existing `dto.Money`.

### Which visits each projection uses

| projection | plan status | visits |
|---|---|---|
| Occurrences | `active` (otherwise an empty list) | cadence visits in `[max(from, today), to)`, each with its linked `job_id` or null |
| Projected invoices | `active` (otherwise an empty list) | the same visits **minus** those with a linked job |
| Projected estimate | `active` or `paused`; `canceled` → `ErrPlanCanceled` (409) | **all** cadence visits in `[max(from, today), to)`, booked or not |
| Forecast recurring series | `active` | the same visits as projected invoices, summed by `Subtotal` |

- Window cap: at most 24 months per plan-level request (`model.ErrWindowTooLarge`, 400); at most
  12 months for the customer-level route.
- Linked visits are loaded in **one** query for every plan involved:
  `SELECT recurring_plan_id, occurrence_date, id FROM jobs WHERE recurring_plan_id = ANY(@plan_ids) AND occurrence_date >= @from AND occurrence_date < @to`.
  The repository returns `map[planID]map["YYYY-MM-DD"]jobID`. Keys are date strings, so time-zone
  differences between `time.Time` values can't break the lookup.

## 6. API

Routes are added in `internal/handler/router.go`. Every route declares its query keys with `allowQuery`.

| method + path | purpose | success |
|---|---|---|
| `POST /api/v1/recurring-plans` | create | 201 + plan |
| `GET /api/v1/recurring-plans?customer_id=&status=&limit=&after=` | list, keyset on `id` ascending; `limit` default 50, max 200 | 200 `{items, next_cursor}` |
| `GET /api/v1/recurring-plans/:id` | one plan | 200 |
| `PATCH /api/v1/recurring-plans/:id` | partial update | 200 |
| `POST /api/v1/recurring-plans/:id/pause` · `/resume` · `/cancel` | status transitions | 200 |
| `GET /api/v1/recurring-plans/:id/occurrences?from=&to=` | visits with `job_id` | 200 |
| `GET /api/v1/recurring-plans/:id/projected-invoices?from=&to=` | projected invoices | 200 |
| `GET /api/v1/recurring-plans/:id/projected-estimate?from=&to=` | projected estimate | 200 |
| `GET /api/v1/customers/:id/recurring-occurrences?from=&to=` | every active plan of a customer, merged by date then plan ID | 200 |

- `from`/`to` are `YYYY-MM-DD`, and `to` is the **last included day** (the same as `dto.DateRange.End`). The
  handler converts it to half-open `[from, to+1day)`. Defaults: `from` = today; `to` = `from` + 3 months for
  occurrences and invoices, + 12 months for the estimate. Responses echo the resolved `range`.
- Projection lists are bounded by the window caps instead of being cursor-paginated (worst case about
  730 rows for a daily plan over 24 months).

### Create body: `dto.CreateRecurringPlanRequest`
```go
CustomerID       int64   `json:"customer_id"        binding:"required,min=1"`
LocationID       *int64  `json:"location_id"        binding:"omitempty,min=1"`
ServiceID        int64   `json:"service_id"         binding:"required,min=1"`
StaffID          *int64  `json:"staff_id"           binding:"omitempty,min=1"`
IntervalUnit     string  `json:"interval_unit"      binding:"required,oneof=day week month"`
IntervalCount    int     `json:"interval_count"     binding:"required,min=1,max=365"`
StartDate        string  `json:"start_date"         binding:"required,datetime=2006-01-02"`
EndDate          *string `json:"end_date"           binding:"omitempty,datetime=2006-01-02"`
Quantity         *string `json:"quantity"`           // decimal string, default "1"
UnitPrice        *string `json:"unit_price"`         // decimal string, default services.price
TaxRate          *string `json:"tax_rate"`           // decimal string 0..1, default "0"
PaymentTermsDays *int    `json:"payment_terms_days" binding:"omitempty,min=0,max=365"`
```
Money and rates are decimal strings. Parse or range errors give 400 (`handler.ErrInvalidBody`). A missing
or inactive service, or a location from another customer, gives 400 `model.ErrInvalidReference`. The
repository maps pg `23503` FK violations to it, so pgx errors never reach the handler.

### PATCH and the cadence lock
- Always editable: `location_id`, `staff_id`, `end_date`, `quantity`, `unit_price`, `tax_rate`, `payment_terms_days`.
- Cadence fields (`interval_unit`, `interval_count`, `start_date`) can only change while the plan has **no
  linked jobs**. Otherwise 409 `model.ErrCadenceLocked`, because linked jobs are keyed by
  `occurrence_date` and a new cadence would orphan them and count visits twice. To change a cadence in use,
  cancel the plan and create a new one.
- `customer_id`, `service_id` and `status` are rejected in PATCH (400).
- Runs in `WithTx`: `SELECT … FOR UPDATE` on the plan, check for linked jobs, update.

### Status transitions
`active ⇄ paused`, `active | paused → canceled`. Any other transition gives 409
`model.ErrInvalidTransition`. A transition to the plan's current status is idempotent (200). Runs in
`WithTx` with `FOR UPDATE`.

### Response DTOs: `internal/dto/recurring.go`
- `RecurringPlanResponse`: every column plus `service_name`; money via `dto.Money`; dates `YYYY-MM-DD`.
- `OccurrenceResponse {plan_id, index, date, job_id|null}`.
- `ProjectedInvoiceResponse {plan_id, ref, index, issued_on, due_on, line{description, quantity, unit_price, amount}, subtotal, tax, total, kind: "projected"}`.
- `ProjectedEstimateResponse {plan_id, range, visit_count, line, subtotal, tax, total, kind: "projected"}`.

### Error mapping (still only in `internal/handler/errors.go`)
| error | status |
|---|---|
| `handler.ErrInvalidBody`, `model.ErrInvalidReference`, `model.ErrWindowTooLarge`, `recurrence.ErrInvalidRule` | 400 |
| `model.ErrPlanNotFound` | 404 |
| `model.ErrPlanCanceled`, `model.ErrInvalidTransition`, `model.ErrCadenceLocked` | 409 |

Implemented as `notFoundErrors` and `conflictErrors` lists next to the existing `badRequestErrors`.

### Files
- `internal/handler/recurring.go`: `RecurringHandler` plus a narrow `RecurringService` interface.
- `internal/service/recurring.go`: CRUD, transitions, projections, `MonthlyRecurring`; depends on a
  `RecurringRepository` interface.
- `internal/repository/recurring.go` + `internal/repository/queries/recurring.sql` (sqlc): `CreatePlan`,
  `GetPlan`, `GetPlanForUpdate`, `ListPlans` (keyset), `UpdatePlan`, `SetPlanStatus`, `PlanHasLinkedJobs`,
  `ActivePlansForCustomer`, `ActivePlansOverlapping`, `LinkedOccurrences`. Every call uses
  `DBQueryTimeout` and reads its connection or transaction from the context, like `DashboardRepository`.
- `cmd/api/main.go`: wiring only.

## 7. Sales Forecast integration

`GET /api/v1/dashboard/sales-forecast?year=` gets its own response type (`dto.YearSeriesResponse` stays
as it is for Revenue per Month):

```json
{
  "range": {"preset": "year", "start": "2026-01-01", "end": "2026-12-31"},
  "total": "12000.00",
  "recurring_total": "4800.00",
  "months": [{"month": "2026-11", "amount": "1000.00", "recurring": "400.00"}]
}
```

- `amount`/`total` keep their current meaning (booked jobs, `MonthlyForecast`), so existing clients
  aren't affected. `recurring`/`recurring_total` are new.
- Recurring = for every active plan, visits in `[max(range.Start, today), range.End)` minus visits with a
  linked job, each adding `ProjectInvoice(...).Subtotal` (tax excluded, the same as the dashboard's
  revenue rule). Past months have `recurring = 0`. A visit counts in the local month of its date.
- Flow: `DashboardService.SalesForecast` runs inside one `WithTx(ReadOnly, Snapshot)`. It calls the
  existing `repo.MonthlyForecast` for booked work, then a new `RecurringForecaster.MonthlyRecurring(ctx, from, to)`
  interface (implemented by `RecurringService`: `ActivePlansOverlapping` + one `LinkedOccurrences` +
  the engine). Both series go through `fillMonths` and are zipped into
  `model.ForecastSeries{Months []ForecastMonth{Month, Booked, Recurring}, BookedTotal, RecurringTotal}`.
  The shared snapshot means a job linked between the two reads can't be counted twice or missed.
- Scale: plans × visits per year in memory (5,000 weekly plans is about 260k date calculations, a few ms).
  Caching is a follow-up.

## 8. Testing

| layer | location | kind | focus |
|---|---|---|---|
| engine | `tests/unit/recurrence/` | table-driven | `At` per unit and count; Jan 31 and Feb 29 clamping; `Between` window edges (before Start, after End, End inclusive, `to` exclusive, empty, mid-cycle); DST in `America/New_York`; property: strictly increasing, inside the window, `Date == At(Index)`; `Validate` |
| model | `tests/unit/model/` | table-driven | amount rounding (1.5 × 33.33 = 50.00), tax half-up, rate 0, due date across a month end, `Ref`, estimate = Σ invoices (1/3/52 visits, rate 0.0825) |
| service | `tests/unit/service/` | mockery | status rules per projection, linked visits skipped vs kept, clamp to today, window caps, transitions, cadence lock, default price, inactive service, error wrapping, single `LinkedOccurrences` call, forecast zip + snapshot tx |
| handler | `tests/unit/handler/` | `httptest` + mock | binding/validation per field, `allowQuery`, `to` inclusive→exclusive, defaults, error→status, response shapes, forecast `amount`/`total` unchanged |
| schema | `tests/integration/schema/` | real Postgres | 000007 up/down/up; cross-customer location, bad interval/end/tax, half-set link pair, duplicate link all rejected |
| repository + endpoints | `tests/integration/recurring/` | real Postgres + `httptest` | CRUD, keyset paging, PATCH lock with/without a linked job, transitions and 409s, occurrences show `job_id`, invoices skip the linked visit while the estimate keeps it, customer-level merge order |
| forecast | `tests/integration/dashboard/forecast_recurring_test.go` | real Postgres + `httptest` | plan + one linked job: no double counting; existing forecast tests unchanged |

Shared tools: a fixed clock through `daterange.NewResolver(tz, fixedNow)` everywhere (no `time.Now()` in
tests); new mockery entries for `RecurringRepository`, `RecurringForecaster` and `RecurringService` in
`.mockery.yml`; a `seedPlan` helper in `tests/integration/recurring/setup_test.go`; demo seed data
(`internal/devdata`) gains a few plans (weekly, every 2 weeks, monthly on the 31st) with linked jobs,
checked in `tests/unit/devdata/demo_test.go`.

Definition of done: `go tool task test`, `go tool task test:integration`, and `go tool task check` all
pass, with no lint rules disabled.
