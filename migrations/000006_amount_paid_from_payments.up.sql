-- The payments table is the source of truth for what an invoice has been paid.
-- documents.amount_paid (and so balance) is derived from it by triggers and can't be written directly.

-- Any write to documents.amount_paid is replaced by the sum of the document's payments.
CREATE FUNCTION documents_derive_amount_paid() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.amount_paid := (SELECT coalesce(sum(p.amount), 0) FROM payments p WHERE p.document_id = NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER documents_amount_paid_derived
    BEFORE INSERT OR UPDATE OF amount_paid ON documents
    FOR EACH ROW EXECUTE FUNCTION documents_derive_amount_paid();

-- Payment changes touch amount_paid on the affected invoices; the trigger above recomputes it.
CREATE FUNCTION payments_sync_amount_paid() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        UPDATE documents SET amount_paid = 0 WHERE id = OLD.document_id;
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        UPDATE documents SET amount_paid = 0 WHERE id = NEW.document_id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER payments_amount_paid_synced
    AFTER INSERT OR DELETE OR UPDATE OF document_id, amount ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_sync_amount_paid();

-- Backfill from payments. Fails on the CHECK below if existing payments overpay an invoice;
-- fix those rows by hand first.
UPDATE documents SET amount_paid = 0;

-- Overpayment is rejected rather than turning into a negative balance that drops out of aging.
ALTER TABLE documents ADD CONSTRAINT documents_amount_paid_within_total CHECK (amount_paid <= total);
