-- Recurring service plans. A plan stores only the rule (every N days/weeks/months from start_date);
-- its future visits and invoices are projected on read, never stored in advance. A visit becomes a
-- real row only when a job is created for it, linked back by jobs.(recurring_plan_id, occurrence_date).
-- Money is numeric(12,2) in dollars; statuses use text + CHECK as in 000001.

-- Target for the plans' composite FK, so a plan can only use a location of its own customer.
ALTER TABLE locations ADD CONSTRAINT locations_id_customer_id_key UNIQUE (id, customer_id);

CREATE TABLE recurring_plans (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id         bigint        NOT NULL REFERENCES customers (id),
    location_id         bigint,
    service_id          bigint        NOT NULL REFERENCES services (id),
    -- Default assignee shown on projected visits.
    staff_id            bigint        REFERENCES staff (id),
    status              text          NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'paused', 'canceled')),
    -- (week, 2) = every 2 weeks.
    interval_unit       text          NOT NULL CHECK (interval_unit IN ('day', 'week', 'month')),
    interval_count      int           NOT NULL CHECK (interval_count > 0),
    -- Anchor and first visit: a local date in BUSINESS_TZ.
    start_date          date          NOT NULL,
    -- Inclusive; NULL = open-ended.
    end_date            date,
    quantity            numeric(10,2) NOT NULL DEFAULT 1 CHECK (quantity > 0),
    -- Copied from services.price on create, then owned by the plan.
    unit_price          numeric(12,2) NOT NULL CHECK (unit_price >= 0),
    -- A fraction: 0.0825 = 8.25%.
    tax_rate            numeric(6,4)  NOT NULL DEFAULT 0 CHECK (tax_rate BETWEEN 0 AND 1),
    -- Projected due date = visit date + N days.
    payment_terms_days  int           NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0),
    created_at          timestamptz   NOT NULL DEFAULT now(),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    CHECK (end_date >= start_date),
    -- A plan can't use another customer's location.
    FOREIGN KEY (location_id, customer_id) REFERENCES locations (id, customer_id)
);
CREATE INDEX recurring_plans_customer_id_idx ON recurring_plans (customer_id);
CREATE INDEX recurring_plans_location_id_idx ON recurring_plans (location_id);
CREATE INDEX recurring_plans_service_id_idx ON recurring_plans (service_id);
CREATE INDEX recurring_plans_staff_id_idx ON recurring_plans (staff_id);
CREATE INDEX recurring_plans_status_start_date_idx ON recurring_plans (status, start_date);

-- The plan visit a job fulfils; occurrence_date is unchanged when the job is rescheduled.
ALTER TABLE jobs
    ADD COLUMN recurring_plan_id bigint REFERENCES recurring_plans (id),
    ADD COLUMN occurrence_date   date,
    ADD CONSTRAINT jobs_recurring_visit_check CHECK ((recurring_plan_id IS NULL) = (occurrence_date IS NULL)),
    -- One job per visit; also serves as the index for the recurring_plan_id FK.
    ADD CONSTRAINT jobs_recurring_visit_key UNIQUE (recurring_plan_id, occurrence_date);
