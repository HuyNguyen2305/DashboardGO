-- name: GetDashboardStats :one
-- Top Stats row. One scalar subquery per metric keeps it to a single round trip.
-- Every range is half-open: [start_at, end_at).
-- Revenue is the subtotal (tax excluded); Total New Jobs is a sales figure and uses the total.
SELECT
    (SELECT coalesce(sum(d.subtotal), 0)
       FROM documents d
      WHERE d.doc_type = 'invoice'
        AND d.status = ANY(@revenue_statuses::text[])
        AND d.issued_at >= @start_at::timestamptz AND d.issued_at < @end_at::timestamptz
    )::numeric AS revenue,

    (SELECT coalesce(sum(p.amount), 0)
       FROM payments p
      WHERE p.received_at >= @start_at::timestamptz AND p.received_at < @end_at::timestamptz
    )::numeric AS collected,

    -- By when the job was finished, not when it was scheduled.
    (SELECT count(*)
       FROM jobs j
      WHERE j.status = 'completed'
        AND j.completed_at >= @start_at::timestamptz AND j.completed_at < @end_at::timestamptz
    )::bigint AS jobs_completed,

    (SELECT coalesce(sum(j.total), 0)
       FROM jobs j
      WHERE j.booked_online
        AND j.status = ANY(@online_job_statuses::text[])
        AND j.created_at >= @start_at::timestamptz AND j.created_at < @end_at::timestamptz
    )::numeric AS new_jobs_booked_online,

    -- Invoices converted from an estimate are skipped: the estimate is already counted.
    (SELECT coalesce(sum(d.total), 0)
       FROM documents d
      WHERE d.issued_at >= @start_at::timestamptz AND d.issued_at < @end_at::timestamptz
        AND (   (d.doc_type = 'estimate' AND d.status = ANY(@new_jobs_estimate_statuses::text[]))
             OR (d.doc_type = 'invoice'  AND d.status = ANY(@new_jobs_invoice_statuses::text[])
                                         AND d.source_document_id IS NULL))
    )::numeric AS total_new_jobs;

-- name: JobStatusBreakdown :many
-- Jobs widget rows: jobs scheduled in the period. No staff_ids and no include_unassigned means all schedules;
-- include_unassigned adds jobs with no staff.
SELECT j.status,
       count(*)::bigint                 AS count,
       coalesce(sum(j.total), 0)::numeric AS amount
  FROM jobs j
 WHERE j.scheduled_at >= @start_at::timestamptz AND j.scheduled_at < @end_at::timestamptz
   AND (   (cardinality(@staff_ids::bigint[]) = 0 AND NOT @include_unassigned::boolean)
        OR j.staff_id = ANY(@staff_ids::bigint[])
        OR (@include_unassigned::boolean AND j.staff_id IS NULL))
 GROUP BY j.status;

-- name: JobDailyCounts :many
-- Jobs widget line chart: jobs per local day and status. Days without jobs are filled in by the service.
SELECT (j.scheduled_at AT TIME ZONE @tz::text)::date AS day,
       j.status,
       count(*)::bigint AS count
  FROM jobs j
 WHERE j.scheduled_at >= @start_at::timestamptz AND j.scheduled_at < @end_at::timestamptz
   AND (   (cardinality(@staff_ids::bigint[]) = 0 AND NOT @include_unassigned::boolean)
        OR j.staff_id = ANY(@staff_ids::bigint[])
        OR (@include_unassigned::boolean AND j.staff_id IS NULL))
 GROUP BY day, j.status
 ORDER BY day, j.status;

-- name: DocumentStatusBreakdown :many
-- Invoices and Estimates widget rows.
SELECT d.status,
       count(*)::bigint                 AS count,
       coalesce(sum(d.total), 0)::numeric AS amount
  FROM documents d
 WHERE d.doc_type = @doc_type::text
   AND d.issued_at >= @start_at::timestamptz AND d.issued_at < @end_at::timestamptz
 GROUP BY d.status;

-- name: InvoiceAging :many
-- Outstanding balances bucketed by days past due as of a local date.
-- Invoices without a due date age from their local issue date. Not yet due (age <= 0) is "current".
-- sqlc.arg() instead of @name: sqlc mis-rewrites two @params inside nested casts on one line.
WITH aged AS (
    SELECT d.balance,
           (sqlc.arg(as_of)::date - coalesce(d.due_date, (d.issued_at AT TIME ZONE sqlc.arg(tz)::text)::date))::int AS age
      FROM documents d
     WHERE d.doc_type = 'invoice'
       AND d.status = ANY(@statuses::text[])
       AND d.balance > 0
)
SELECT (CASE
          WHEN aged.age <= 0  THEN 'current'
          WHEN aged.age <= 30 THEN '1_30'
          WHEN aged.age <= 60 THEN '31_60'
          WHEN aged.age <= 90 THEN '61_90'
          ELSE '90_plus'
        END)::text                          AS bucket,
       count(*)::bigint                      AS count,
       coalesce(sum(aged.balance), 0)::numeric AS amount
  FROM aged
 GROUP BY bucket;

-- name: MonthlyRevenue :many
-- Revenue per Month: same invoice rule as the Stats row (subtotal, tax excluded), per local month.
SELECT date_trunc('month', d.issued_at AT TIME ZONE @tz::text)::date AS month,
       coalesce(sum(d.subtotal), 0)::numeric                    AS amount
  FROM documents d
 WHERE d.doc_type = 'invoice'
   AND d.status = ANY(@revenue_statuses::text[])
   AND d.issued_at >= @start_at::timestamptz AND d.issued_at < @end_at::timestamptz
 GROUP BY month
 ORDER BY month;

-- name: MonthlyForecast :many
-- Sales Forecast: the total of jobs scheduled per local month that are still on the books
-- (job_statuses: not canceled or terminated). Future months show the work already booked.
SELECT date_trunc('month', j.scheduled_at AT TIME ZONE @tz::text)::date AS month,
       coalesce(sum(j.total), 0)::numeric                         AS amount
  FROM jobs j
 WHERE j.scheduled_at >= @start_at::timestamptz AND j.scheduled_at < @end_at::timestamptz
   AND j.status = ANY(@job_statuses::text[])
 GROUP BY month
 ORDER BY month;

-- name: PaymentsByMethod :many
-- Payments widget rows, biggest amount first.
SELECT p.method,
       count(*)::bigint                  AS count,
       coalesce(sum(p.amount), 0)::numeric AS amount
  FROM payments p
 WHERE p.received_at >= @start_at::timestamptz AND p.received_at < @end_at::timestamptz
 GROUP BY p.method
 ORDER BY amount DESC, p.method;

-- name: RevenueByStaff :many
-- Revenue by Staff: invoice -> job -> staff. A NULL staff (no job, or a job without staff) is the "Other" row.
-- Revenue is the subtotal (tax excluded), as in the Stats row.
SELECT s.id                                  AS staff_id,
       s.name                                AS staff_name,
       s.avatar_url,
       count(*)::bigint                      AS count,
       coalesce(sum(d.subtotal), 0)::numeric AS amount
  FROM documents d
  LEFT JOIN jobs  j ON j.id = d.job_id
  LEFT JOIN staff s ON s.id = j.staff_id
 WHERE d.doc_type = 'invoice'
   AND d.status = ANY(@revenue_statuses::text[])
   AND d.issued_at >= @start_at::timestamptz AND d.issued_at < @end_at::timestamptz
 GROUP BY s.id, s.name, s.avatar_url
 ORDER BY amount DESC, s.name NULLS LAST;

-- name: ActiveServices :many
-- Active Services: jobs in the given statuses grouped by service. A NULL service is the "No service" row.
-- count is visits (jobs); customer_count is the distinct customers behind them. A recurring visit is
-- one on a service marked recurring or one fulfilling a recurring plan, so a row can mix both kinds.
SELECT sv.id                               AS service_id,
       sv.name                             AS service_name,
       coalesce(sv.is_recurring, false)::boolean AS is_recurring,
       count(*)::bigint                    AS count,
       count(*) FILTER (WHERE coalesce(sv.is_recurring, false) OR j.recurring_plan_id IS NOT NULL)::bigint AS recurring_count,
       count(DISTINCT j.customer_id)::bigint AS customer_count,
       coalesce(sum(j.total), 0)::numeric  AS amount
  FROM jobs j
  LEFT JOIN services sv ON sv.id = j.service_id
 WHERE j.status = ANY(@statuses::text[])
   AND j.scheduled_at >= @start_at::timestamptz AND j.scheduled_at < @end_at::timestamptz
 GROUP BY sv.id, sv.name, sv.is_recurring
 ORDER BY amount DESC, sv.name NULLS LAST;

-- name: ActiveServiceCustomers :one
-- Active Services footer: distinct customers overall and with a recurring visit (as in ActiveServices).
-- A customer with several services counts once, so these can't be summed from the per-service rows.
SELECT count(DISTINCT j.customer_id)::bigint AS customer_count,
       count(DISTINCT j.customer_id)
         FILTER (WHERE coalesce(sv.is_recurring, false) OR j.recurring_plan_id IS NOT NULL)::bigint AS recurring_customer_count
  FROM jobs j
  LEFT JOIN services sv ON sv.id = j.service_id
 WHERE j.status = ANY(@statuses::text[])
   AND j.scheduled_at >= @start_at::timestamptz AND j.scheduled_at < @end_at::timestamptz;

-- name: CustomerTagUsage :many
-- Tags widget, Customer scope: how many customers carry each tag in use.
SELECT t.id, t.name, count(*)::bigint AS count
  FROM tags t
  JOIN customer_tags ct ON ct.tag_id = t.id
 WHERE t.scope = 'customer'
 GROUP BY t.id, t.name
 ORDER BY count DESC, t.name;

-- name: LocationTagUsage :many
-- Tags widget, Location scope: how many locations carry each tag in use, and how many distinct customers own them.
SELECT t.id, t.name, count(*)::bigint AS count, count(DISTINCT l.customer_id)::bigint AS customer_count
  FROM tags t
  JOIN location_tags lt ON lt.tag_id = t.id
  JOIN locations l ON l.id = lt.location_id
 WHERE t.scope = 'location'
 GROUP BY t.id, t.name
 ORDER BY count DESC, t.name;

-- name: CustomersBySource :many
-- New Customers & Leads by Source: customers of every status per local month and source (NULL source = 'na').
SELECT date_trunc('month', c.created_at AT TIME ZONE @tz::text)::date AS month,
       coalesce(c.source, 'na')::text                              AS source,
       count(*)::bigint                                            AS count
  FROM customers c
 WHERE c.created_at >= @start_at::timestamptz AND c.created_at < @end_at::timestamptz
 GROUP BY month, coalesce(c.source, 'na')
 ORDER BY month, source;
