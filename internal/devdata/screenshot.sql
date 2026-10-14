-- Screenshot preset: rebuilds the numbers from the dashboard screenshots
-- (October 2026, GMT+7, today = Oct 12), plus rows that must be ignored:
-- wrong status, wrong month, or already counted elsewhere.
-- Loaded by `cmd/seed --preset=screenshot` and by the dashboard integration tests.
-- Expects an empty database whose ID counters start at 1.

INSERT INTO staff (name) VALUES ('Thọ'), ('User'), ('Schedule 3'), ('Schedule 4');
-- Fixed created_at: the default now() would follow the real clock.
INSERT INTO customers (name, status, created_at) VALUES ('Seed Customer', 'active', '2026-01-15 10:00+07');

-- Services; the two recurring ones make up "Recurring Services" in Active Services.
INSERT INTO services (name, price, is_recurring, interval_days) VALUES
  ('Blank Service',       73.44, false, NULL),
  ('Initial Service',     73.44, false, NULL),
  ('Bi-Monthly Service',  73.44, true,  60),
  ('Every 21 Days',       73.44, true,  21);

-- Invoices issued in October.
INSERT INTO documents (doc_type, status, number, customer_id, issued_at, total) VALUES
  ('invoice',  'sent',     'INV-1',  1, '2026-10-03 10:00+07',  555.14),  -- counts; no due date, so it ages from Oct 3
  ('invoice',  'paid',     'INV-2',  1, '2026-10-05 10:00+07',  892.26),  -- counts
  ('invoice',  'draft',    'INV-3',  1, '2026-10-06 10:00+07', 1378.64),  -- ignored by revenue: draft
  ('invoice',  'void',     'INV-4',  1, '2026-10-07 10:00+07',   65.00),  -- ignored by revenue: void
  ('invoice',  'paid',     'INV-5',  1, '2026-09-30 23:59+07',  500.00);  -- September

-- Estimates: four in October, one in November.
INSERT INTO documents (doc_type, status, number, customer_id, issued_at, total) VALUES
  ('estimate', 'draft',    'EST-1',  1, '2026-10-02 09:00+07',  660.22),
  ('estimate', 'pending',  'EST-2',  1, '2026-10-04 09:00+07',   62.00),
  ('estimate', 'invoiced', 'EST-3',  1, '2026-10-08 09:00+07',   40.00),
  ('estimate', 'lost',     'EST-4',  1, '2026-10-09 09:00+07',  300.00),  -- ignored by Total New Jobs: lost
  ('estimate', 'won',      'EST-5',  1, '2026-11-05 10:00+07',  150.00);  -- November: Total New Jobs only (the forecast is built from jobs)

-- Invoice converted from EST-3: counts as revenue, but not again in Total New Jobs.
INSERT INTO documents (doc_type, status, number, customer_id, issued_at, total, source_document_id)
SELECT 'invoice', 'paid', 'INV-6', 1, '2026-10-10 09:00+07', 40.00, id
  FROM documents WHERE doc_type = 'estimate' AND number = 'EST-3';

-- Older invoices for Invoices Aging (as of Oct 12) and the yearly charts.
-- amount_paid is derived from the payments below.
INSERT INTO documents (doc_type, status, number, customer_id, issued_at, due_date, total) VALUES
  ('invoice', 'partial_paid', 'INV-A3', 1, '2026-06-01 10:00+07', '2026-06-01', 300.00),  -- 133 days: 90+, balance 200
  ('invoice', 'sent',         'INV-A2', 1, '2026-07-01 00:30+07', '2026-07-20',  95.00),  -- 84 days: 61–90; July locally, June in UTC
  ('invoice', 'partial_paid', 'INV-A1', 1, '2026-08-03 10:00+07', '2026-10-20', 200.00),  -- not due yet: current, balance 92.77
  ('invoice', 'sent',         'INV-A6', 1, '2026-08-10 10:00+07', '2026-09-12',  10.00),  -- exactly 30 days: 1–30
  ('invoice', 'sent',         'INV-A7', 1, '2026-08-11 10:00+07', '2026-09-11',  20.00),  -- 31 days: 31–60
  ('invoice', 'paid',         'INV-A5', 1, '2026-08-12 10:00+07', '2026-08-30',  75.00),  -- paid off: not aging
  ('invoice', 'draft',        'INV-A4', 1, '2026-08-13 10:00+07', '2026-05-01',  50.00);  -- draft: not aging

-- Revenue is the subtotal (tax excluded); every screenshot document is tax-free.
UPDATE documents SET subtotal = total, tax = 0;

-- Payments: 11 checks of 65.00 plus one Stripe payment in October, one check in September,
-- and the earlier payments behind the aging balances.
INSERT INTO payments (document_id, method, amount, received_at)
SELECT d.id, 'check', 65.00, '2026-10-01 08:00+07'::timestamptz + n * interval '1 day'
  FROM documents d, generate_series(0, 10) n
 WHERE d.number = 'INV-2';
INSERT INTO payments (document_id, method, amount, received_at)
SELECT d.id, v.method, v.amount, v.received_at
  FROM (VALUES ('INV-2',  'stripe', 152.26, '2026-10-10 15:00+07'::timestamptz),  -- before "today" (Oct 12)
               ('INV-5',  'check',   99.00, '2026-09-30 12:00+07'::timestamptz),
               ('INV-A3', 'check',  100.00, '2026-06-15 10:00+07'::timestamptz),
               ('INV-A1', 'card',   107.23, '2026-08-20 10:00+07'::timestamptz),
               ('INV-A5', 'cash',    75.00, '2026-08-25 10:00+07'::timestamptz)) v(number, method, amount, received_at)
  JOIN documents d ON d.doc_type = 'invoice' AND d.number = v.number;

-- Jobs in October: 14 completed for Thọ (scheduled Oct 1–14, 09:00), totalling 924.30.
-- Each is finished an hour after its slot; the Oct 13 and 14 jobs were done early, on Oct 12 at 08:00.
INSERT INTO jobs (customer_id, staff_id, status, total, scheduled_at, completed_at, created_at)
SELECT 1, (SELECT id FROM staff WHERE name = 'Thọ'), 'completed',
       CASE WHEN n = 13 THEN 66.04 ELSE 66.02 END,
       '2026-10-01 09:00+07'::timestamptz + n * interval '1 day',
       least('2026-10-01 10:00+07'::timestamptz + n * interval '1 day', '2026-10-12 08:00+07'),
       '2026-09-28 09:00+07'::timestamptz + n * interval '1 day'
  FROM generate_series(0, 13) n;

-- 16 incomplete for User (Oct 15–30 at 00:30 local, which is still the previous day in UTC), totalling 1175.06.
-- Services: 8 Blank, 4 Bi-Monthly, 2 Every 21 Days, 2 Initial.
INSERT INTO jobs (customer_id, staff_id, service_id, status, total, scheduled_at, created_at)
SELECT 1, (SELECT id FROM staff WHERE name = 'User'),
       (SELECT id FROM services WHERE name = CASE WHEN n < 8  THEN 'Blank Service'
                                                  WHEN n < 12 THEN 'Bi-Monthly Service'
                                                  WHEN n < 14 THEN 'Every 21 Days'
                                                  ELSE 'Initial Service' END),
       'incomplete',
       CASE WHEN n = 15 THEN 73.46 ELSE 73.44 END,
       '2026-10-15 00:30+07'::timestamptz + n * interval '1 day',
       '2026-10-01 09:00+07'
  FROM generate_series(0, 15) n;

-- Unassigned jobs. October totals: 18 incomplete = 1371.24, 14 completed = 924.30, 1 terminated = 65.00.
INSERT INTO jobs (customer_id, status, total, scheduled_at, completed_at, created_at, booked_online) VALUES
  (1, 'incomplete',  76.18, '2026-10-20 09:00+07', NULL,                  '2026-10-01 09:00+07', false),
  (1, 'terminated',  65.00, '2026-10-21 09:00+07', NULL,                  '2026-10-01 09:00+07', false),
  (1, 'completed',   50.00, '2026-11-01 00:00+07', '2026-11-01 01:00+07', '2026-10-25 09:00+07', false),  -- November
  (1, 'incomplete', 120.00, '2026-10-02 09:00+07', NULL,                  '2026-09-15 09:00+07', true);   -- booked online in September

-- Active Services: the 120.00 job is a third Initial Service job; the 76.18 job has no service.
UPDATE jobs SET service_id = (SELECT id FROM services WHERE name = 'Initial Service') WHERE total = 120.00;

-- Revenue by Staff links: INV-2 -> a Thọ job; INV-6 -> the job with no staff ("Other");
-- INV-1 stays without a job ("Other" too).
UPDATE documents SET job_id = (SELECT id FROM jobs
                                WHERE staff_id = (SELECT id FROM staff WHERE name = 'Thọ')
                                ORDER BY scheduled_at LIMIT 1)
 WHERE number = 'INV-2';
UPDATE documents SET job_id = (SELECT id FROM jobs WHERE total = 76.18) WHERE number = 'INV-6';

-- New Customers & Leads by Source (2026, local months): Jan na 1 (Seed Customer), Jun na 2,
-- Aug google 1 (00:30 local on Aug 1 = Jul 31 in UTC), Sep na 21, Oct na 10 + facebook 1.
INSERT INTO customers (name, status, source, created_at)
SELECT 'June lead ' || n, 'lead', NULL, '2026-06-10 10:00+07'::timestamptz + n * interval '1 hour'
  FROM generate_series(1, 2) n;
INSERT INTO customers (name, status, source, created_at)
SELECT 'September lead ' || n, 'lead', NULL, '2026-09-01 10:00+07'::timestamptz + n * interval '1 day'
  FROM generate_series(1, 21) n;
INSERT INTO customers (name, status, source, created_at)
SELECT 'October lead ' || n, 'lead', NULL, '2026-10-01 10:00+07'::timestamptz + n * interval '1 day'
  FROM generate_series(1, 10) n;
INSERT INTO customers (name, status, source, created_at) VALUES
  ('Google Customer',   'active', 'google',   '2026-08-01 00:30+07'),
  ('Facebook Customer', 'active', 'facebook', '2026-10-05 10:00+07'),
  ('Last Year Yelp',    'active', 'yelp',     '2025-12-31 23:00+07');  -- not in 2026

-- Tags. Location: 1233 and a456 used once each (50% / 50%); "unused" must not be listed.
-- Customer: vip on 2 customers, late-payer on 1.
INSERT INTO locations (customer_id, address) VALUES (1, '1 Main St'), (1, '2 Side St');
INSERT INTO tags (name, scope) VALUES
  ('1233', 'location'), ('a456', 'location'), ('unused', 'location'),
  ('vip', 'customer'), ('late-payer', 'customer');
INSERT INTO location_tags (location_id, tag_id)
SELECT l.id, t.id FROM locations l JOIN tags t
    ON t.scope = 'location' AND (l.address, t.name) IN (('1 Main St', '1233'), ('2 Side St', 'a456'));
INSERT INTO customer_tags (customer_id, tag_id)
SELECT c.id, t.id FROM customers c JOIN tags t
    ON t.scope = 'customer' AND (c.name, t.name) IN
       (('Seed Customer', 'vip'), ('Google Customer', 'vip'), ('Facebook Customer', 'late-payer'));
