-- name: GetServicePrice :one
SELECT price, is_active FROM services WHERE id = @id;

-- name: CustomerExists :one
SELECT EXISTS (SELECT 1 FROM customers WHERE id = @id)::boolean;

-- name: CreateRecurringPlan :one
-- The repository re-reads the row by id to join the service name.
INSERT INTO recurring_plans (
        customer_id, location_id, service_id, staff_id, interval_unit, interval_count,
        start_date, end_date, quantity, unit_price, tax_rate, payment_terms_days
    ) VALUES (
        @customer_id, @location_id, @service_id, @staff_id, @interval_unit, @interval_count,
        @start_date, @end_date, @quantity, @unit_price, @tax_rate, @payment_terms_days
    )
RETURNING id;

-- name: GetRecurringPlan :one
SELECT sqlc.embed(p), s.name AS service_name
  FROM recurring_plans p JOIN services s ON s.id = p.service_id
 WHERE p.id = @id;

-- name: GetRecurringPlanForUpdate :one
-- Locks only the plan row: FOR UPDATE OF p leaves the services row alone.
SELECT sqlc.embed(p), s.name AS service_name
  FROM recurring_plans p JOIN services s ON s.id = p.service_id
 WHERE p.id = @id
   FOR UPDATE OF p;

-- name: ListRecurringPlans :many
-- Keyset on id; a NULL filter matches everything.
SELECT sqlc.embed(p), s.name AS service_name
  FROM recurring_plans p JOIN services s ON s.id = p.service_id
 WHERE p.id > @after
   AND (sqlc.narg('customer_id')::bigint IS NULL OR p.customer_id = sqlc.narg('customer_id'))
   AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
 ORDER BY p.id
 LIMIT @lim;

-- name: UpdateRecurringPlan :one
UPDATE recurring_plans SET
        location_id = @location_id,
        staff_id = @staff_id,
        interval_unit = @interval_unit,
        interval_count = @interval_count,
        start_date = @start_date,
        end_date = @end_date,
        quantity = @quantity,
        unit_price = @unit_price,
        tax_rate = @tax_rate,
        payment_terms_days = @payment_terms_days,
        updated_at = now()
     WHERE id = @id
RETURNING id;

-- name: SetRecurringPlanStatus :one
UPDATE recurring_plans SET status = @status, updated_at = now() WHERE id = @id
RETURNING id;

-- name: RecurringPlanHasLinkedJobs :one
SELECT EXISTS (SELECT 1 FROM jobs WHERE recurring_plan_id = @id::bigint)::boolean;

-- name: ActiveRecurringPlansOverlapping :many
-- Active plans whose [start_date, end_date] overlaps the half-open window [from, to).
SELECT sqlc.embed(p), s.name AS service_name
  FROM recurring_plans p JOIN services s ON s.id = p.service_id
 WHERE p.status = 'active'
   AND p.start_date < @to_date::date
   AND (p.end_date IS NULL OR p.end_date >= @from_date::date)
 ORDER BY p.id;

-- name: ActiveRecurringPlansForCustomer :many
SELECT sqlc.embed(p), s.name AS service_name
  FROM recurring_plans p JOIN services s ON s.id = p.service_id
 WHERE p.customer_id = @customer_id
   AND p.status = 'active'
   AND p.start_date < @to_date::date
   AND (p.end_date IS NULL OR p.end_date >= @from_date::date)
 ORDER BY p.id;

-- name: LinkedRecurringVisits :many
-- Jobs fulfilling plan visits in [from, to), for all plans in one query.
SELECT recurring_plan_id, occurrence_date, id
  FROM jobs
 WHERE recurring_plan_id = ANY(@plan_ids::bigint[])
   AND occurrence_date >= @from_date::date AND occurrence_date < @to_date::date;
