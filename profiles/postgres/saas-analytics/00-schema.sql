-- SaaS product-analytics warehouse.
--
-- Money convention: EVERY monetary column is an integer count of
-- cents (BIGINT), never a float. mrr_cents, amount_cents, etc.
-- Divide by 100.0 in the BI layer if you want dollars.
--
-- Time convention: event/transaction timestamps are timestamptz.
-- created_at on dimension rows is timestamptz too. A few "as of a
-- calendar day" columns are date.
--
-- All base tables use small dense integer PKs (serial/bigserial) so
-- the high-volume seed can wire foreign keys with cheap modulo
-- arithmetic ((g % parent_count) + 1) and stay set-based. One table
-- (feature_events) also carries a gen_random_uuid() event_id to show
-- a uuid column; gen_random_uuid() is built into Postgres 16 core, no
-- extension needed.
--
-- The heavy analytics (joins / windows / percentiles / cohorts) lives
-- entirely in the v_* VIEWS in 20-views.sql. These base tables are
-- deliberately normalized; the views flatten them for a
-- single-source visualizer.

-- ─────────────────────────── ENUM TYPES ───────────────────────────

CREATE TYPE plan_tier          AS ENUM ('free', 'starter', 'growth', 'business', 'enterprise');
CREATE TYPE billing_interval   AS ENUM ('monthly', 'annual');
CREATE TYPE subscription_status AS ENUM ('trialing', 'active', 'past_due', 'canceled', 'paused');
CREATE TYPE invoice_status     AS ENUM ('draft', 'open', 'paid', 'void', 'uncollectible');
CREATE TYPE payment_status     AS ENUM ('succeeded', 'failed', 'refunded', 'pending');
CREATE TYPE payment_method     AS ENUM ('card', 'ach', 'wire', 'paypal');
CREATE TYPE ticket_priority    AS ENUM ('low', 'normal', 'high', 'urgent');
CREATE TYPE ticket_status      AS ENUM ('open', 'pending', 'resolved', 'closed');
CREATE TYPE event_kind         AS ENUM ('page_view', 'feature_use', 'api_call', 'export', 'invite', 'error');
CREATE TYPE device_type        AS ENUM ('desktop', 'mobile', 'tablet');
CREATE TYPE signup_channel     AS ENUM ('organic', 'paid_search', 'social', 'referral', 'partner', 'outbound');
CREATE TYPE user_role          AS ENUM ('owner', 'admin', 'member', 'viewer');

-- ─────────────────────────── DIMENSIONS ───────────────────────────

-- Countries: ISO code, region, name, and plain numeric lat/long
-- (no PostGIS — the visualizer's map layer reads lat/lng columns).
CREATE TABLE countries (
    id           SERIAL PRIMARY KEY,
    iso_code     TEXT NOT NULL UNIQUE,       -- ISO 3166-1 alpha-2
    name         TEXT NOT NULL,
    region       TEXT NOT NULL,              -- e.g. 'Americas', 'EMEA', 'APAC'
    lat          NUMERIC(8,5) NOT NULL,
    lng          NUMERIC(8,5) NOT NULL
);

-- Plans: the SaaS price book.
CREATE TABLE plans (
    id                SERIAL PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    tier              plan_tier NOT NULL,
    monthly_price_cents BIGINT NOT NULL,     -- list price, monthly
    annual_price_cents  BIGINT NOT NULL,     -- list price, annual (per year)
    seats_included    INT NOT NULL,
    features          TEXT[] NOT NULL DEFAULT '{}',   -- text[] of feature flags
    is_active         BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Accounts: customer orgs.
CREATE TABLE accounts (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    slug           CITEXT NOT NULL UNIQUE,            -- case-insensitive handle
    country_id     INT NOT NULL REFERENCES countries(id),
    industry       TEXT NOT NULL,
    employee_count INT NOT NULL,
    signup_channel signup_channel NOT NULL,
    signup_ip      INET,                              -- inet column
    metadata       JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags           TEXT[] NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_accounts_country ON accounts(country_id);
CREATE INDEX idx_accounts_created ON accounts(created_at);
CREATE INDEX idx_accounts_channel ON accounts(signup_channel);

-- Users: members of an account.
CREATE TABLE users (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT NOT NULL REFERENCES accounts(id),
    email        CITEXT NOT NULL,                     -- case-insensitive email
    full_name    TEXT NOT NULL,
    role         user_role NOT NULL,
    is_active    BOOLEAN NOT NULL DEFAULT true,
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_users_account   ON users(account_id);
CREATE INDEX idx_users_created   ON users(created_at);
CREATE INDEX idx_users_last_seen ON users(last_seen_at);

-- ─────────────────────────── BILLING ──────────────────────────────

-- Subscriptions: one (or more, over time) per account.
CREATE TABLE subscriptions (
    id             BIGSERIAL PRIMARY KEY,
    account_id     BIGINT NOT NULL REFERENCES accounts(id),
    plan_id        INT NOT NULL REFERENCES plans(id),
    status         subscription_status NOT NULL,
    billing_interval billing_interval NOT NULL,
    mrr_cents      BIGINT NOT NULL,            -- normalized monthly recurring revenue
    seats          INT NOT NULL,
    trial_length   INTERVAL,                   -- interval column (trial duration)
    started_at     TIMESTAMPTZ NOT NULL,
    canceled_at    TIMESTAMPTZ,                -- non-null iff status = 'canceled'
    created_at     TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_subs_account  ON subscriptions(account_id);
CREATE INDEX idx_subs_plan     ON subscriptions(plan_id);
CREATE INDEX idx_subs_status   ON subscriptions(status);
CREATE INDEX idx_subs_started  ON subscriptions(started_at);
CREATE INDEX idx_subs_canceled ON subscriptions(canceled_at);

-- Subscription items: per-feature/add-on lines on a subscription.
CREATE TABLE subscription_items (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id),
    product         TEXT NOT NULL,             -- 'base', 'seats', 'storage', 'support', 'sso'
    quantity        INT NOT NULL,
    unit_price_cents BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_subitems_sub ON subscription_items(subscription_id);

-- Invoices: one per billing period per account.
CREATE TABLE invoices (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT NOT NULL REFERENCES accounts(id),
    subscription_id BIGINT REFERENCES subscriptions(id),
    status       invoice_status NOT NULL,
    amount_cents BIGINT NOT NULL,             -- total, in cents
    tax_cents    BIGINT NOT NULL DEFAULT 0,
    currency     TEXT NOT NULL DEFAULT 'usd',
    issued_at    TIMESTAMPTZ NOT NULL,
    due_at       TIMESTAMPTZ,
    paid_at      TIMESTAMPTZ,
    metadata     JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_invoices_account ON invoices(account_id);
CREATE INDEX idx_invoices_sub     ON invoices(subscription_id);
CREATE INDEX idx_invoices_status  ON invoices(status);
CREATE INDEX idx_invoices_issued  ON invoices(issued_at);
CREATE INDEX idx_invoices_paid    ON invoices(paid_at);

-- Invoice line items.
CREATE TABLE invoice_line_items (
    id          BIGSERIAL PRIMARY KEY,
    invoice_id  BIGINT NOT NULL REFERENCES invoices(id),
    description TEXT NOT NULL,
    quantity    INT NOT NULL,
    unit_price_cents BIGINT NOT NULL,
    amount_cents BIGINT NOT NULL              -- quantity * unit_price_cents
);
CREATE INDEX idx_lineitems_invoice ON invoice_line_items(invoice_id);

-- Payments: attempts against invoices.
CREATE TABLE payments (
    id           BIGSERIAL PRIMARY KEY,
    invoice_id   BIGINT NOT NULL REFERENCES invoices(id),
    account_id   BIGINT NOT NULL REFERENCES accounts(id),
    status       payment_status NOT NULL,
    method       payment_method NOT NULL,
    amount_cents BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_payments_invoice ON payments(invoice_id);
CREATE INDEX idx_payments_account ON payments(account_id);
CREATE INDEX idx_payments_status  ON payments(status);
CREATE INDEX idx_payments_created ON payments(created_at);

-- ─────────────────────────── PRODUCT USAGE ────────────────────────

-- Feature / usage events. HIGH VOLUME (~250k). props is jsonb.
CREATE TABLE feature_events (
    id         BIGSERIAL PRIMARY KEY,
    event_id   UUID NOT NULL DEFAULT gen_random_uuid(),  -- uuid column
    user_id    BIGINT NOT NULL REFERENCES users(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    kind       event_kind NOT NULL,
    feature    TEXT NOT NULL,
    props      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ts         TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_events_user    ON feature_events(user_id);
CREATE INDEX idx_events_account ON feature_events(account_id);
CREATE INDEX idx_events_kind    ON feature_events(kind);
CREATE INDEX idx_events_feature ON feature_events(feature);
CREATE INDEX idx_events_ts      ON feature_events(ts);

-- Sessions (~60k). duration in seconds; carries device + country.
CREATE TABLE sessions (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id),
    account_id      BIGINT NOT NULL REFERENCES accounts(id),
    device_type     device_type NOT NULL,
    country_id      INT NOT NULL REFERENCES countries(id),
    duration_seconds INT NOT NULL,
    page_count      INT NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_account ON sessions(account_id);
CREATE INDEX idx_sessions_device  ON sessions(device_type);
CREATE INDEX idx_sessions_country ON sessions(country_id);
CREATE INDEX idx_sessions_started ON sessions(started_at);

-- ─────────────────────────── SUPPORT / VOC ────────────────────────

-- Support tickets.
CREATE TABLE support_tickets (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id),
    priority    ticket_priority NOT NULL,
    status      ticket_status NOT NULL,
    subject     TEXT NOT NULL,
    csat        INT,                          -- 1..5, nullable until resolved
    opened_at   TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ
);
CREATE INDEX idx_tickets_account  ON support_tickets(account_id);
CREATE INDEX idx_tickets_status   ON support_tickets(status);
CREATE INDEX idx_tickets_priority ON support_tickets(priority);
CREATE INDEX idx_tickets_opened   ON support_tickets(opened_at);

-- NPS responses (score 0..10).
CREATE TABLE nps_responses (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT NOT NULL REFERENCES accounts(id),
    user_id      BIGINT NOT NULL REFERENCES users(id),
    score        INT NOT NULL CHECK (score BETWEEN 0 AND 10),
    comment      TEXT,
    submitted_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_nps_account   ON nps_responses(account_id);
CREATE INDEX idx_nps_submitted ON nps_responses(submitted_at);

-- ─────────────────────────── LIFECYCLE FLOWS ──────────────────────

-- Plan changes (upgrade/downgrade) — feeds the sankey view.
CREATE TABLE plan_changes (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT NOT NULL REFERENCES accounts(id),
    from_plan_id INT NOT NULL REFERENCES plans(id),
    to_plan_id   INT NOT NULL REFERENCES plans(id),
    mrr_delta_cents BIGINT NOT NULL,          -- signed: + expansion, - contraction
    changed_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_planchanges_account ON plan_changes(account_id);
CREATE INDEX idx_planchanges_changed ON plan_changes(changed_at);

-- Onboarding events — one row per (account, step). Feeds the funnel.
CREATE TABLE onboarding_events (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id),
    step_order  INT NOT NULL,                 -- 1..6
    step_name   TEXT NOT NULL,
    completed   BOOLEAN NOT NULL,
    ts          TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_onboarding_account ON onboarding_events(account_id);
CREATE INDEX idx_onboarding_step    ON onboarding_events(step_order);
CREATE INDEX idx_onboarding_ts      ON onboarding_events(ts);
