-- Allows "partial" again. Rows merged into "partial_paid" by the up migration stay merged.
ALTER TABLE documents DROP CONSTRAINT documents_status_check;
ALTER TABLE documents ADD CONSTRAINT documents_check CHECK (
    (doc_type = 'invoice'  AND status IN ('draft', 'sent', 'partial', 'partial_paid', 'paid', 'void', 'write_off', 'failed'))
 OR (doc_type = 'estimate' AND status IN ('draft', 'pending', 'won', 'invoiced', 'lost'))
);
