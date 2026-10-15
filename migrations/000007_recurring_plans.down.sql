ALTER TABLE jobs
    DROP CONSTRAINT jobs_recurring_visit_key,
    DROP CONSTRAINT jobs_recurring_visit_check,
    DROP COLUMN occurrence_date,
    DROP COLUMN recurring_plan_id;
DROP TABLE recurring_plans;
ALTER TABLE locations DROP CONSTRAINT locations_id_customer_id_key;
