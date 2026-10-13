-- Money is numeric(12,2) in dollars. Statuses use text + CHECK so new values only need a migration, not an enum rewrite.

CREATE TABLE staff (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL,
    avatar_url  text,
    is_active   boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customers (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL,
    email       text,
    phone       text,
    status      text        NOT NULL DEFAULT 'lead'
                CHECK (status IN ('lead', 'active', 'inactive')),
    -- NULL source is shown as "NA" on the dashboard.
    source      text
                CHECK (source IN ('yelp', 'angieslist', 'craigslist', 'facebook', 'google', 'referral')),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customers_created_at_source_idx ON customers (created_at, source);

CREATE TABLE locations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id  bigint      NOT NULL REFERENCES customers (id),
    address      text        NOT NULL,
    city         text,
    state        text,
    zip          text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX locations_customer_id_idx ON locations (customer_id);

CREATE TABLE tags (
    id     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name   text NOT NULL,
    scope  text NOT NULL CHECK (scope IN ('customer', 'location')),
    UNIQUE (scope, name),
    -- Target for the composite FKs below, so a tag can only be linked within its own scope.
    UNIQUE (id, scope)
);

CREATE TABLE customer_tags (
    customer_id  bigint NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    tag_id       bigint NOT NULL,
    tag_scope    text   NOT NULL DEFAULT 'customer' CHECK (tag_scope = 'customer'),
    PRIMARY KEY (customer_id, tag_id),
    FOREIGN KEY (tag_id, tag_scope) REFERENCES tags (id, scope) ON DELETE CASCADE
);
CREATE INDEX customer_tags_tag_id_idx ON customer_tags (tag_id);

CREATE TABLE location_tags (
    location_id  bigint NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    tag_id       bigint NOT NULL,
    tag_scope    text   NOT NULL DEFAULT 'location' CHECK (tag_scope = 'location'),
    PRIMARY KEY (location_id, tag_id),
    FOREIGN KEY (tag_id, tag_scope) REFERENCES tags (id, scope) ON DELETE CASCADE
);
CREATE INDEX location_tags_tag_id_idx ON location_tags (tag_id);

CREATE TABLE services (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name           text          NOT NULL,
    price          numeric(12,2) NOT NULL DEFAULT 0 CHECK (price >= 0),
    is_recurring   boolean       NOT NULL DEFAULT false,
    interval_days  int           CHECK (interval_days > 0),
    is_active      boolean       NOT NULL DEFAULT true,
    CHECK (is_recurring OR interval_days IS NULL)
);

CREATE TABLE jobs (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id    bigint        NOT NULL REFERENCES customers (id),
    location_id    bigint        REFERENCES locations (id),
    service_id     bigint        REFERENCES services (id),
    -- A "schedule" on the dashboard is a staff member.
    staff_id       bigint        REFERENCES staff (id),
    status         text          NOT NULL DEFAULT 'incomplete'
                   CHECK (status IN ('incomplete', 'completed', 'reschedule', 'canceled', 'terminated', 'workpool')),
    booked_online  boolean       NOT NULL DEFAULT false,
    total          numeric(12,2) NOT NULL DEFAULT 0 CHECK (total >= 0),
    scheduled_at   timestamptz   NOT NULL,
    completed_at   timestamptz,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    CHECK (status = 'completed' OR completed_at IS NULL)
);
CREATE INDEX jobs_status_scheduled_at_idx ON jobs (status, scheduled_at);
CREATE INDEX jobs_staff_id_scheduled_at_idx ON jobs (staff_id, scheduled_at);
CREATE INDEX jobs_customer_id_idx ON jobs (customer_id);
CREATE INDEX jobs_location_id_idx ON jobs (location_id);
CREATE INDEX jobs_service_id_idx ON jobs (service_id);

-- Invoices and estimates share one table, told apart by doc_type.
CREATE TABLE documents (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_type            text          NOT NULL CHECK (doc_type IN ('invoice', 'estimate')),
    status              text          NOT NULL DEFAULT 'draft',
    number              text          NOT NULL,
    customer_id         bigint        NOT NULL REFERENCES customers (id),
    job_id              bigint        REFERENCES jobs (id),
    -- Estimate this invoice was created from.
    source_document_id  bigint        REFERENCES documents (id),
    issued_at           timestamptz   NOT NULL DEFAULT now(),
    due_date            date,
    expires_at          date,
    subtotal            numeric(12,2) NOT NULL DEFAULT 0 CHECK (subtotal >= 0),
    tax                 numeric(12,2) NOT NULL DEFAULT 0 CHECK (tax >= 0),
    total               numeric(12,2) NOT NULL DEFAULT 0 CHECK (total >= 0),
    amount_paid         numeric(12,2) NOT NULL DEFAULT 0 CHECK (amount_paid >= 0),
    balance             numeric(12,2) GENERATED ALWAYS AS (total - amount_paid) STORED,
    UNIQUE (doc_type, number),
    -- Target for payments' composite FK, so payments can only reference invoices.
    UNIQUE (id, doc_type),
    CHECK (
        (doc_type = 'invoice'  AND status IN ('draft', 'sent', 'partial', 'partial_paid', 'paid', 'void', 'write_off', 'failed'))
     OR (doc_type = 'estimate' AND status IN ('draft', 'pending', 'won', 'invoiced', 'lost'))
    ),
    CHECK (doc_type = 'invoice'  OR (due_date IS NULL AND amount_paid = 0)),
    CHECK (doc_type = 'estimate' OR expires_at IS NULL)
);
CREATE INDEX documents_type_status_issued_at_idx ON documents (doc_type, status, issued_at);
CREATE INDEX documents_unpaid_due_date_idx ON documents (due_date)
    WHERE doc_type = 'invoice' AND balance > 0;
CREATE INDEX documents_customer_id_idx ON documents (customer_id);
CREATE INDEX documents_job_id_idx ON documents (job_id);
CREATE INDEX documents_source_document_id_idx ON documents (source_document_id);

CREATE TABLE document_items (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id  bigint        NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    service_id   bigint        REFERENCES services (id),
    description  text          NOT NULL,
    quantity     numeric(10,2) NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_price   numeric(12,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    amount       numeric(12,2) GENERATED ALWAYS AS (round(quantity * unit_price, 2)) STORED
);
CREATE INDEX document_items_document_id_idx ON document_items (document_id);
CREATE INDEX document_items_service_id_idx ON document_items (service_id);

CREATE TABLE payments (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id  bigint        NOT NULL,
    doc_type     text          NOT NULL DEFAULT 'invoice' CHECK (doc_type = 'invoice'),
    method       text          NOT NULL CHECK (method IN ('check', 'stripe', 'cash', 'card', 'other')),
    amount       numeric(12,2) NOT NULL CHECK (amount > 0),
    received_at  timestamptz   NOT NULL DEFAULT now(),
    FOREIGN KEY (document_id, doc_type) REFERENCES documents (id, doc_type)
);
CREATE INDEX payments_received_at_idx ON payments (received_at);
CREATE INDEX payments_document_id_idx ON payments (document_id);
