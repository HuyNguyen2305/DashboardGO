-- "Jobs Completed" counts by completion time, so every completed job needs one.
-- Jobs completed before this column was filled are assumed done at their scheduled time.
UPDATE jobs SET completed_at = scheduled_at WHERE status = 'completed' AND completed_at IS NULL;

ALTER TABLE jobs DROP CONSTRAINT jobs_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_completed_at_check CHECK ((status = 'completed') = (completed_at IS NOT NULL));

CREATE INDEX jobs_completed_at_idx ON jobs (completed_at) WHERE status = 'completed';
