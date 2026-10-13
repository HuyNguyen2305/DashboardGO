-- Widgets that break data down across every status filter only on the date column.
-- The existing (status, date) indexes can't serve those range scans on PostgreSQL 16.
CREATE INDEX jobs_scheduled_at_idx ON jobs (scheduled_at);
CREATE INDEX documents_type_issued_at_idx ON documents (doc_type, issued_at);
