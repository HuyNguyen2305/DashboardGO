-- amount_paid keeps its last derived values and becomes writable again.
ALTER TABLE documents DROP CONSTRAINT documents_amount_paid_within_total;
DROP TRIGGER payments_amount_paid_synced ON payments;
DROP FUNCTION payments_sync_amount_paid();
DROP TRIGGER documents_amount_paid_derived ON documents;
DROP FUNCTION documents_derive_amount_paid();
