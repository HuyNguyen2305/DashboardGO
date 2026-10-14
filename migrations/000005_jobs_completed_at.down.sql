-- The completed_at backfill is kept: it is valid under the old constraint too.
DROP INDEX jobs_completed_at_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_completed_at_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_check CHECK (status = 'completed' OR completed_at IS NULL);
