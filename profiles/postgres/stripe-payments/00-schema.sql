-- Stripe-shape payments. Names and statuses follow the public
-- API verbatim where possible so muscle memory carries over.

CREATE TABLE customers (
  id           TEXT PRIMARY KEY,                  -- cus_*
  email        TEXT NOT NULL,
  name         TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payment_methods (
  id           TEXT PRIMARY KEY,                  -- pm_*
  customer_id  TEXT NOT NULL REFERENCES customers(id),
  type         TEXT NOT NULL,                     -- card, us_bank_account, sepa_debit, link
  brand        TEXT,                              -- visa, mastercard, ...
  last4        TEXT,
  exp_month    INT,
  exp_year     INT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pm_customer ON payment_methods(customer_id);

CREATE TABLE payment_intents (
  id                 TEXT PRIMARY KEY,            -- pi_*
  customer_id        TEXT NOT NULL REFERENCES customers(id),
  payment_method_id  TEXT REFERENCES payment_methods(id),
  amount_cents       BIGINT NOT NULL,
  currency           TEXT NOT NULL DEFAULT 'usd',
  status             TEXT NOT NULL,               -- requires_payment_method, requires_action, processing, succeeded, canceled
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  captured_at        TIMESTAMPTZ
);
CREATE INDEX idx_pi_customer ON payment_intents(customer_id);
CREATE INDEX idx_pi_status   ON payment_intents(status);
CREATE INDEX idx_pi_created  ON payment_intents(created_at);

CREATE TABLE charges (
  id                  TEXT PRIMARY KEY,           -- ch_*
  payment_intent_id   TEXT NOT NULL REFERENCES payment_intents(id),
  amount_cents        BIGINT NOT NULL,
  currency            TEXT NOT NULL DEFAULT 'usd',
  status              TEXT NOT NULL,              -- pending, succeeded, failed
  failure_code        TEXT,                       -- card_declined, expired_card, insufficient_funds, ...
  failure_message     TEXT,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ch_pi      ON charges(payment_intent_id);
CREATE INDEX idx_ch_status  ON charges(status);
CREATE INDEX idx_ch_created ON charges(created_at);

CREATE TABLE refunds (
  id            TEXT PRIMARY KEY,                 -- re_*
  charge_id     TEXT NOT NULL REFERENCES charges(id),
  amount_cents  BIGINT NOT NULL,
  reason        TEXT,                             -- duplicate, fraudulent, requested_by_customer
  status        TEXT NOT NULL DEFAULT 'succeeded',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_re_charge  ON refunds(charge_id);

CREATE TABLE disputes (
  id            TEXT PRIMARY KEY,                 -- dp_*
  charge_id     TEXT NOT NULL REFERENCES charges(id),
  amount_cents  BIGINT NOT NULL,
  reason        TEXT NOT NULL,                    -- fraudulent, unrecognized, duplicate, ...
  status        TEXT NOT NULL,                    -- needs_response, under_review, won, lost
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dp_status ON disputes(status);

-- Ledger of net cash movement, the table the finance team reads.
CREATE TABLE balance_transactions (
  id            TEXT PRIMARY KEY,                 -- txn_*
  source_type   TEXT NOT NULL,                    -- charge, refund, dispute, fee, payout
  source_id     TEXT NOT NULL,
  amount_cents  BIGINT NOT NULL,                  -- signed; refunds are negative
  fee_cents     BIGINT NOT NULL DEFAULT 0,
  net_cents     BIGINT NOT NULL,                  -- amount - fee
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bt_source  ON balance_transactions(source_type, source_id);
CREATE INDEX idx_bt_created ON balance_transactions(created_at);
