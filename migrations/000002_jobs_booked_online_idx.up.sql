-- Supports "New Jobs Booked Online": jobs filtered by booking time.
CREATE INDEX jobs_booked_online_created_at_idx ON jobs (created_at) WHERE booked_online;
