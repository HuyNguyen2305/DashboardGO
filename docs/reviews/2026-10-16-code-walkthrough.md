# Code walkthrough — 2026-10-16

A manual review of the dashboard and recurring-plans code for business-logic mistakes. The source of truth was the code, its tests and, for recurring plans, the design spec (`docs/superpowers/specs/2026-10-15-recurring-plans-design.md`). The findings were grouped into sections, and each section was planned and approved in plan mode before any change was made.

**Scope:** `internal/handler`, `service`, `repository` (+ SQL), `model`, `dto`, `daterange`, `recurrence`, `middleware`, `config`, `cmd/api`, migrations. Dev tooling (`devdata`, `seed`, `migrate`, `fmthook`) was not reviewed.

## A. Dashboard: which date range each widget counts
| # | Finding | Decision | Change |
|---|---|---|---|
| A1 | Revenue per Month counted invoices dated after now; the Stats Revenue card did not, so the current month's bar could disagree with "This Month" Revenue | Cap at now | `RevenuePerMonth(ctx, r, until)`: the query is capped and all 12 months are still listed |
| A2 | Jobs and Active Services are not capped, with no comment or test saying it's deliberate | Keep uncapped: upcoming jobs belong in them | Comments + handler test |
| A3 | The caps on Invoices, Estimates and Revenue by Staff were untested | — | `TestPeriodWidgetsForwardTheRightRange`; `TestPeriodWidgetsStopAtNow` extended |

## B. Dashboard: which statuses count
| # | Finding | Decision | Change |
|---|---|---|---|
| B1 | Nothing pinned that `failed` invoices are not revenue (Aging counts them as owed) | Not revenue | `TestFailedInvoicesAreNotRevenue` |
| B2 | Active Services counted only `incomplete` jobs | Count all open jobs: `incomplete`, `reschedule`, `workpool` | `model.ActiveJobStatuses` |
| B3a | "Recurring" came only from `services.is_recurring` | Recurring service **or** job linked to a plan | SQL predicate; new per-row `recurring_count` (additive JSON field) |
| B3b | Plans can use non-recurring services | Allowed | — |

## C. Recurring plans: projections
| # | Finding | Decision | Change |
|---|---|---|---|
| C1 | The projected estimate echoed the requested window, not the period actually priced (from today on); a window entirely in the past built an inverted period | Echo the priced period (estimate only); a past window returns 200 with zero visits | `NewProjectedEstimateResponse(e)` builds the range from `e.From/To` |
| C2 | The estimate line's `quantity × unit_price` can differ by cents from `amount` | As specified (amount = Σ rounded invoices) | Comments |
| C3 | Paused or canceled plans: empty lists, but an estimate (409 when canceled) | As specified (spec §5, D6) | — |

## D. Recurring plans: lifecycle
| # | Finding | Decision | Change |
|---|---|---|---|
| D1 | Moving `end_date` earlier, or canceling, leaves jobs linked after the new end | As specified: jobs are real bookings | Comments, spec note, `TestAPIEndDateAndCancelKeepLinkedJobs` |
| D2 | Any linked job, even a past one, locked the cadence forever | Lock only on linked visits dated today or later | `PlanHasLinkedJobsFrom(ctx, id, today)`; 409 text now says "upcoming linked jobs"; spec §6 updated |

## E. API surface
| # | Finding | Decision | Change |
|---|---|---|---|
| E1 | No route checks authentication, including the recurring-plan writes; CORS allows an `Authorization` header that nothing reads | **Open** | — |
| E2 | `staff_ids=Unassigned` is a 400; an empty `staff_ids=` means all staff | Keep both | Comment + test cases |

## F. Naming and docs
| # | Finding | Decision | Change |
|---|---|---|---|
| F1 | `new_jobs_booked_online` and `total_new_jobs` are money sums but read like counts | Document; keep the JSON names | DTO/model comments |
| F2 | Invoices and Estimates sum totals with tax; Revenue uses the subtotal | Document | DTO comment |

## Checked and correct
- Snapshot transactions, including the dashboard and recurring repositories sharing one transaction for the Sales Forecast.
- Error mapping.
- The aging balance, which is derived by a trigger.
- No double counting in Revenue by Staff or Total New Jobs.
- `Shares` rounding.
- DST-safe date and recurrence math, and month-end clamping.
- The composite FK on plan locations.
- Keyset paging and PATCH null handling.
- CORS, request IDs, config validation and graceful shutdown.

## Open items
1. **E1, authentication.** Decide whether a gateway authenticates requests in front of the API or the app does it. Until then, every route, writes included, is unauthenticated. An in-app solution needs its own design.
2. **The integration tests haven't been run yet** for these changes: `go tool task test:integration` with `TEST_POSTGRES_URL`.
3. **Client-visible changes:**
   - Active Services adds a per-row `recurring_count`, and the header recurring counts can rise where plan-linked jobs sit on non-recurring services.
   - The projected estimate's `range` can now start later than the requested `from`.
   - The 409 text for the cadence lock has changed.
