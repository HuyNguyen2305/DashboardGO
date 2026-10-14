-- "partial" and "partial_paid" meant the same thing; keep "partial_paid".
UPDATE documents SET status = 'partial_paid' WHERE status = 'partial';

ALTER TABLE documents DROP CONSTRAINT documents_check;
ALTER TABLE documents ADD CONSTRAINT documents_status_check CHECK (
    (doc_type = 'invoice'  AND status IN ('draft', 'sent', 'partial_paid', 'paid', 'void', 'write_off', 'failed'))
 OR (doc_type = 'estimate' AND status IN ('draft', 'pending', 'won', 'invoiced', 'lost'))
);
