-- Synthetic seed. Generated procedurally so this file stays small.
-- The two seeded "bugs" are noted inline below.

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

-- customers + payment_methods
INSERT INTO customers (id, email, name, created_at)
SELECT
  'cus_' || lpad(g::text, 8, '0'),
  'user' || g || '@example.com',
  'User ' || g,
  now() - (random() * interval '120 days')
FROM generate_series(1, fab_seed_int('payments_customers', 200)) g;

INSERT INTO payment_methods (id, customer_id, type, brand, last4, exp_month, exp_year)
SELECT
  'pm_' || lpad(g::text, 10, '0'),
  'cus_' || lpad(((g - 1) % cfg.customers + 1)::text, 8, '0'),
  (ARRAY['card','card','card','card','us_bank_account','sepa_debit','link'])[1 + (g % 7)],
  (ARRAY['visa','mastercard','amex','discover'])[1 + (g % 4)],
  lpad((1000 + (g * 37) % 9000)::text, 4, '0'),
  1 + (g % 12),
  2025 + (g % 5)
FROM (
  SELECT fab_seed_int('payments_customers', 200) AS customers,
         fab_seed_int('payments_methods', 240) AS methods
) cfg,
generate_series(1, cfg.methods) g;

-- payment_intents: ~1000, spread over 90 days, mostly succeeded
INSERT INTO payment_intents (id, customer_id, payment_method_id, amount_cents, status, created_at, captured_at)
SELECT
  'pi_' || lpad(g::text, 10, '0'),
  'cus_' || lpad((1 + (g % cfg.customers))::text, 8, '0'),
  'pm_' || lpad((1 + (g % cfg.methods))::text, 10, '0'),
  ((100 + (g * 13) % 50000))::bigint,
  CASE
    WHEN g % 100 = 0 THEN 'canceled'
    WHEN g %  50 = 0 THEN 'requires_action'
    WHEN g %  40 = 0 THEN 'requires_payment_method'
    WHEN g %  35 = 0 THEN 'processing'
    ELSE 'succeeded'
  END,
  now() - ((g * '0.13 hours'::interval)),
  CASE WHEN g % 100 = 0 OR g % 50 = 0 OR g % 40 = 0 OR g % 35 = 0
       THEN NULL ELSE now() - ((g * '0.13 hours'::interval)) + interval '2 seconds' END
FROM (
  SELECT fab_seed_int('payments_customers', 200) AS customers,
         fab_seed_int('payments_methods', 240) AS methods,
         fab_seed_int('payments_intents', 1000) AS intents
) cfg,
generate_series(1, cfg.intents) g;

-- Bug seed #1: bump six payment intents into requires_action and
-- backdate them past the 24h window. The agent should find these
-- with a `status='requires_action' AND created_at < now() - 1d`.
UPDATE payment_intents
   SET status = 'requires_action',
       created_at = now() - interval '5 days',
       captured_at = NULL
 WHERE id IN ('pi_0000000017','pi_0000000123','pi_0000000287',
              'pi_0000000451','pi_0000000729','pi_0000000883');

-- charges: one per non-canceled intent, mostly succeeded
INSERT INTO charges (id, payment_intent_id, amount_cents, status, failure_code, failure_message, created_at)
SELECT
  'ch_' || lpad(g::text, 10, '0'),
  pi.id,
  pi.amount_cents,
  CASE
    WHEN pi.status = 'succeeded'                               THEN 'succeeded'
    WHEN pi.status IN ('canceled','requires_payment_method')   THEN 'failed'
    ELSE 'pending'
  END,
  CASE WHEN pi.status IN ('canceled','requires_payment_method')
       THEN (ARRAY['card_declined','expired_card','insufficient_funds'])[1 + (g % 3)]
       ELSE NULL END,
  CASE WHEN pi.status IN ('canceled','requires_payment_method')
       THEN 'Issuer declined the transaction'
       ELSE NULL END,
  pi.created_at + interval '1 second'
FROM (SELECT id, amount_cents, status, created_at,
             row_number() OVER (ORDER BY id) AS g
        FROM payment_intents
       WHERE status NOT IN ('requires_action')) pi;

-- refunds: ~6% of successful charges get a partial-to-full refund
INSERT INTO refunds (id, charge_id, amount_cents, reason, created_at)
SELECT
  're_' || lpad(g::text, 10, '0'),
  c.id,
  (c.amount_cents * (50 + (g % 51)) / 100)::bigint,
  (ARRAY['duplicate','fraudulent','requested_by_customer'])[1 + (g % 3)],
  c.created_at + interval '1 day' + (random() * interval '5 days')
FROM (SELECT id, amount_cents, created_at,
             row_number() OVER (ORDER BY id) AS g
        FROM charges
       WHERE status = 'succeeded') c
WHERE g % 17 = 0;

-- disputes: a small handful, mostly resolved
INSERT INTO disputes (id, charge_id, amount_cents, reason, status, created_at)
SELECT
  'dp_' || lpad(g::text, 6, '0'),
  c.id,
  c.amount_cents,
  (ARRAY['fraudulent','unrecognized','duplicate','product_not_received'])[1 + (g % 4)],
  (ARRAY['won','lost','under_review','needs_response'])[1 + (g % 4)],
  c.created_at + interval '7 days'
FROM (SELECT id, amount_cents, created_at,
             row_number() OVER (ORDER BY id) AS g
        FROM charges
       WHERE status = 'succeeded') c
WHERE g % 230 = 0;

-- balance_transactions: ledger entries for each charge + refund.
-- Fees are 2.9% + $0.30 (Stripe's headline rate).
INSERT INTO balance_transactions (id, source_type, source_id, amount_cents, fee_cents, net_cents, created_at)
SELECT
  'txn_c_' || c.id,
  'charge',
  c.id,
  c.amount_cents,
  GREATEST(1, (c.amount_cents * 29 / 1000) + 30),
  c.amount_cents - GREATEST(1, (c.amount_cents * 29 / 1000) + 30),
  c.created_at
FROM charges c
WHERE c.status = 'succeeded';

INSERT INTO balance_transactions (id, source_type, source_id, amount_cents, fee_cents, net_cents, created_at)
SELECT
  'txn_r_' || r.id,
  'refund',
  r.id,
  -r.amount_cents,
  0,
  -r.amount_cents,
  r.created_at
FROM refunds r;

-- Bug seed #2: delete the ledger row for one refund. The charges
-- ledger still shows the refund; balance_transactions doesn't —
-- a real-world reconciliation gap from a failed background job.
DELETE FROM balance_transactions
 WHERE id = (SELECT 'txn_r_' || id FROM refunds ORDER BY id LIMIT 1);
