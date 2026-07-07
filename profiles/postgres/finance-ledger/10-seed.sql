-- Chart of accounts.
INSERT INTO accounts (code, name, type, normal_side, description) VALUES
    ('1000', 'Cash',                   'asset',     'debit',  'Operating checking account'),
    ('1010', 'Petty Cash',              'asset',     'debit',  NULL),
    ('1100', 'Accounts Receivable',     'asset',     'debit',  'Money owed by customers'),
    ('1200', 'Inventory',               'asset',     'debit',  NULL),
    ('1500', 'Equipment',               'asset',     'debit',  'Owned hardware/laptops'),
    ('1510', 'Accumulated Depreciation','asset',     'credit', 'Contra-asset'),
    ('2000', 'Accounts Payable',        'liability', 'credit', 'Money owed to vendors'),
    ('2100', 'Credit Card Payable',     'liability', 'credit', NULL),
    ('2300', 'Payroll Liabilities',     'liability', 'credit', 'Withholding due to tax authority'),
    ('2500', 'Long-Term Debt',          'liability', 'credit', NULL),
    ('3000', 'Common Stock',            'equity',    'credit', NULL),
    ('3100', 'Retained Earnings',       'equity',    'credit', NULL),
    ('4000', 'Product Revenue',         'income',    'credit', NULL),
    ('4100', 'Service Revenue',         'income',    'credit', NULL),
    ('4900', 'Other Income',            'income',    'credit', NULL),
    ('5000', 'Cost of Goods Sold',      'expense',   'debit',  NULL),
    ('6000', 'Salaries',                'expense',   'debit',  NULL),
    ('6100', 'Rent',                    'expense',   'debit',  NULL),
    ('6200', 'Utilities',               'expense',   'debit',  NULL),
    ('6300', 'Software Subscriptions',  'expense',   'debit',  NULL),
    ('6400', 'Marketing',               'expense',   'debit',  NULL),
    ('6500', 'Travel',                  'expense',   'debit',  NULL),
    ('6600', 'Office Supplies',         'expense',   'debit',  NULL),
    ('6900', 'Bank Fees',               'expense',   'debit',  NULL),
    ('7000', 'Depreciation Expense',    'expense',   'debit',  NULL);

-- Helper: lookup account id by code (used heavily below).
-- A view would be simpler than inlining (SELECT ...) every time.
CREATE OR REPLACE VIEW acct AS SELECT code, id FROM accounts;

CREATE OR REPLACE FUNCTION fab_seed_int(setting_key TEXT, fallback INTEGER)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
    raw TEXT;
BEGIN
    raw := current_setting('fab.seed.' || setting_key, true);
    IF raw IS NULL OR btrim(raw) = '' THEN
        RETURN fallback;
    END IF;
    RETURN raw::INTEGER;
EXCEPTION WHEN invalid_text_representation THEN
    RAISE EXCEPTION 'invalid integer seed setting %.%=%', 'fab.seed', setting_key, raw;
END $$;

-- Opening capital: founders inject $250,000 cash for common stock.
WITH j AS (
    INSERT INTO journals (posted_at, memo, reference)
    VALUES ('2024-01-02 09:00+00', 'Initial capital injection', 'FOUNDING-001')
    RETURNING id
)
INSERT INTO entries (journal_id, account_id, side, amount_cents, memo)
SELECT j.id, a.id, s.side, 25000000, 'Opening' FROM j, accounts a
JOIN (VALUES ('1000','debit'::normal_side), ('3000','credit'::normal_side)) AS s(code, side)
    ON s.code = a.code;

-- ============================================================
-- Procedurally generated journals across 2024.
-- 1000 small revenue + 1000 small expense + 12 monthly rent +
-- 26 bi-weekly payroll. Enough volume for non-trivial GROUP BY
-- and time-series queries.
-- ============================================================

-- 1000 product sales: customer pays cash, we recognize revenue.
DO $$
DECLARE
    i INTEGER;
    jid INTEGER;
    amt BIGINT;
    ts TIMESTAMPTZ;
BEGIN
    FOR i IN 1..fab_seed_int('ledger_product_sales', 1000) LOOP
        amt := (30 + (random() * 200)::INTEGER)::BIGINT * 100;  -- $30..$230 in cents
        ts := TIMESTAMPTZ '2024-01-03 00:00+00' + (random() * 365) * INTERVAL '1 day';
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, 'Product sale', 'INV-' || lpad(i::TEXT, 6, '0'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='1000'), 'debit',  amt, NULL),
            (jid, (SELECT id FROM acct WHERE code='4000'), 'credit', amt, NULL);
    END LOOP;
END $$;

-- 600 service revenue invoices: A/R, customer pays later.
DO $$
DECLARE
    i INTEGER;
    jid INTEGER;
    amt BIGINT;
    ts TIMESTAMPTZ;
BEGIN
    FOR i IN 1..fab_seed_int('ledger_service_invoices', 600) LOOP
        amt := (500 + (random() * 4500)::INTEGER)::BIGINT * 100;  -- $500..$5000
        ts := TIMESTAMPTZ '2024-01-08 00:00+00' + (random() * 350) * INTERVAL '1 day';
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, 'Service invoice issued', 'SVC-' || lpad(i::TEXT, 6, '0'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='1100'), 'debit',  amt, NULL),
            (jid, (SELECT id FROM acct WHERE code='4100'), 'credit', amt, NULL);
    END LOOP;
END $$;

-- 400 software subscription expenses (recurring SaaS).
DO $$
DECLARE
    i INTEGER;
    jid INTEGER;
    amt BIGINT;
    ts TIMESTAMPTZ;
    vendors TEXT[] := ARRAY['Slack','Notion','Figma','Linear','GitHub','Datadog','AWS','Snowflake','Zoom','Loom'];
BEGIN
    FOR i IN 1..fab_seed_int('ledger_software_expenses', 400) LOOP
        amt := (20 + (random() * 400)::INTEGER)::BIGINT * 100;
        ts := TIMESTAMPTZ '2024-01-05 00:00+00' + (random() * 360) * INTERVAL '1 day';
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, vendors[1 + (random() * 9)::INTEGER] || ' subscription', 'CC-' || lpad(i::TEXT, 6, '0'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='6300'), 'debit',  amt, NULL),
            (jid, (SELECT id FROM acct WHERE code='2100'), 'credit', amt, NULL);
    END LOOP;
END $$;

-- 12 monthly rent payments.
DO $$
DECLARE m INTEGER; ts TIMESTAMPTZ; jid INTEGER;
BEGIN
    FOR m IN 1..12 LOOP
        ts := make_timestamptz(2024, m, 1, 9, 0, 0);
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, 'Monthly rent', 'RENT-2024-' || lpad(m::TEXT, 2, '0'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='6100'), 'debit',  1200000, NULL),  -- $12,000
            (jid, (SELECT id FROM acct WHERE code='1000'), 'credit', 1200000, NULL);
    END LOOP;
END $$;

-- 26 bi-weekly payroll runs, $48k gross, $9.6k withholding.
DO $$
DECLARE r INTEGER; ts TIMESTAMPTZ; jid INTEGER; gross BIGINT := 4800000; withhold BIGINT := 960000;
BEGIN
    FOR r IN 0..25 LOOP
        ts := TIMESTAMPTZ '2024-01-12 09:00+00' + r * INTERVAL '14 days';
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, 'Payroll run', 'PAY-' || to_char(ts,'YYYY-MM-DD'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='6000'), 'debit',  gross,            'Gross salaries'),
            (jid, (SELECT id FROM acct WHERE code='1000'), 'credit', gross - withhold, 'Net to employees'),
            (jid, (SELECT id FROM acct WHERE code='2300'), 'credit', withhold,         'Tax withheld');
    END LOOP;
END $$;

-- 50 marketing spends — Google Ads / LinkedIn / sponsorships.
DO $$
DECLARE i INTEGER; ts TIMESTAMPTZ; jid INTEGER; amt BIGINT;
    channels TEXT[] := ARRAY['Google Ads','LinkedIn Ads','Conference sponsorship','Podcast ad','SEO contractor'];
BEGIN
    FOR i IN 1..fab_seed_int('ledger_marketing_spends', 50) LOOP
        amt := (200 + (random() * 5000)::INTEGER)::BIGINT * 100;
        ts := TIMESTAMPTZ '2024-01-20 00:00+00' + (random() * 340) * INTERVAL '1 day';
        INSERT INTO journals (posted_at, memo, reference)
        VALUES (ts, channels[1 + (random() * 4)::INTEGER], 'MKT-' || lpad(i::TEXT, 5, '0'))
        RETURNING id INTO jid;
        INSERT INTO entries (journal_id, account_id, side, amount_cents, memo) VALUES
            (jid, (SELECT id FROM acct WHERE code='6400'), 'debit',  amt, NULL),
            (jid, (SELECT id FROM acct WHERE code='2100'), 'credit', amt, NULL);
    END LOOP;
END $$;

-- ============================================================
-- INTENTIONALLY BROKEN JOURNALS — for "find the unbalanced one"
-- agent exercises. Each fails v_journal_balance.net_cents = 0.
-- ============================================================

-- 1) A typoed credit: $1,000 in, $100 out.
WITH j AS (
    INSERT INTO journals (posted_at, memo, reference)
    VALUES ('2024-04-15 14:33+00', 'Customer deposit', 'BAD-001')
    RETURNING id
)
INSERT INTO entries (journal_id, account_id, side, amount_cents, memo)
SELECT j.id, a.id, s.side, s.amt, 'TYPO suspected' FROM j, accounts a
JOIN (VALUES ('1000','debit'::normal_side,100000), ('1100','credit'::normal_side,10000)) AS s(code, side, amt)
    ON s.code = a.code;

-- 2) A missing credit leg entirely (only the debit was posted).
WITH j AS (
    INSERT INTO journals (posted_at, memo, reference)
    VALUES ('2024-07-22 11:00+00', 'Travel reimbursement', 'BAD-002')
    RETURNING id
)
INSERT INTO entries (journal_id, account_id, side, amount_cents, memo)
SELECT j.id, (SELECT id FROM acct WHERE code='6500'), 'debit', 87500, 'Missing credit leg'
  FROM j;

-- 3) Both legs on the same side (double debit).
WITH j AS (
    INSERT INTO journals (posted_at, memo, reference)
    VALUES ('2024-10-03 16:45+00', 'Equipment purchase', 'BAD-003')
    RETURNING id
)
INSERT INTO entries (journal_id, account_id, side, amount_cents, memo)
SELECT j.id, a.id, 'debit', 250000, 'Both debits — fix me' FROM j, accounts a
WHERE a.code IN ('1500', '6300');

ANALYZE;
