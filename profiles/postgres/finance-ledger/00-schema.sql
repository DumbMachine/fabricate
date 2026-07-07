-- Minimal double-entry ledger.
--
-- accounts            chart of accounts. Each row carries a type
--                     (asset|liability|equity|income|expense) and
--                     normal balance side (debit|credit).
-- journals            one journal == one business event. Every
--                     entry inside the journal must net to zero
--                     across debit/credit for the journal to be
--                     "balanced". We don't enforce this with a
--                     trigger — the seed data intentionally
--                     includes a handful of unbalanced journals
--                     for agents to find.
-- entries             individual debit/credit lines belonging to
--                     a journal, posted against one account.
-- v_account_balances  rolled-up debit/credit/net per account.
-- v_journal_balance   per-journal net (zero == balanced).

CREATE TYPE account_type AS ENUM ('asset', 'liability', 'equity', 'income', 'expense');
CREATE TYPE normal_side AS ENUM ('debit', 'credit');

CREATE TABLE accounts (
    id           SERIAL PRIMARY KEY,
    code         TEXT NOT NULL UNIQUE,        -- e.g. '1000'
    name         TEXT NOT NULL,
    type         account_type NOT NULL,
    normal_side  normal_side NOT NULL,
    description  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE journals (
    id           SERIAL PRIMARY KEY,
    posted_at    TIMESTAMPTZ NOT NULL,
    memo         TEXT NOT NULL,
    reference    TEXT,                         -- invoice/receipt id
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id           SERIAL PRIMARY KEY,
    journal_id   INTEGER NOT NULL REFERENCES journals(id) ON DELETE CASCADE,
    account_id   INTEGER NOT NULL REFERENCES accounts(id),
    side         normal_side NOT NULL,
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    memo         TEXT
);

CREATE INDEX entries_journal_idx ON entries (journal_id);
CREATE INDEX entries_account_idx ON entries (account_id);
CREATE INDEX journals_posted_idx ON journals (posted_at);

-- Per-account totals. "net" follows the account's normal side: a
-- positive net for an asset is debit-heavy (good); a positive net
-- for a liability is credit-heavy (also good).
CREATE OR REPLACE VIEW v_account_balances AS
SELECT a.id                                                   AS account_id,
       a.code,
       a.name,
       a.type,
       a.normal_side,
       COALESCE(SUM(CASE WHEN e.side = 'debit'  THEN e.amount_cents END), 0)::BIGINT  AS debit_cents,
       COALESCE(SUM(CASE WHEN e.side = 'credit' THEN e.amount_cents END), 0)::BIGINT  AS credit_cents,
       (COALESCE(SUM(CASE WHEN e.side = a.normal_side    THEN e.amount_cents END), 0)
      - COALESCE(SUM(CASE WHEN e.side <> a.normal_side   THEN e.amount_cents END), 0))::BIGINT AS net_cents
  FROM accounts a
  LEFT JOIN entries e ON e.account_id = a.id
 GROUP BY a.id, a.code, a.name, a.type, a.normal_side;

-- Per-journal balance: a journal is balanced iff net_cents = 0.
-- Agents can `SELECT * FROM v_journal_balance WHERE net_cents <> 0`
-- to find the seeded broken journals.
CREATE OR REPLACE VIEW v_journal_balance AS
SELECT j.id                                                   AS journal_id,
       j.posted_at,
       j.memo,
       COALESCE(SUM(CASE WHEN e.side = 'debit'  THEN e.amount_cents END), 0)::BIGINT  AS debit_cents,
       COALESCE(SUM(CASE WHEN e.side = 'credit' THEN e.amount_cents END), 0)::BIGINT  AS credit_cents,
       (COALESCE(SUM(CASE WHEN e.side = 'debit'  THEN e.amount_cents END), 0)
      - COALESCE(SUM(CASE WHEN e.side = 'credit' THEN e.amount_cents END), 0))::BIGINT AS net_cents
  FROM journals j
  LEFT JOIN entries e ON e.journal_id = j.id
 GROUP BY j.id;
