-- Set-based seed. Every high-volume table is one big
-- INSERT ... SELECT FROM generate_series(...). FKs are wired with
-- modulo arithmetic against known parent counts so we never run a
-- correlated subquery per row. random() drives the spread; a few
-- deliberate skews (Q4 signup spike, 'starter' churn, an NPS dip,
-- US/GB signup dominance, power-law feature usage) make the charts
-- interesting.
--
-- TIME WINDOW: the dataset spans 2023-06-01 .. 2025-05-31 (24 months,
-- "now" being ~2025-06). All timestamps are pinned to literals (not
-- now()) so the seeded patterns land on stable calendar months
-- regardless of when the container boots.

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

-- ════════════════════════════ COUNTRIES (40) ═════════════════════
-- Hand-listed so iso_code / region / lat / lng are real-ish. The seed
-- skews accounts toward country_id 1 (US) and 2 (GB).
INSERT INTO countries (iso_code, name, region, lat, lng) VALUES
  ('US','United States','Americas',  37.09024,-95.71289),
  ('GB','United Kingdom','EMEA',     55.37805, -3.43597),
  ('DE','Germany','EMEA',            51.16569, 10.45153),
  ('FR','France','EMEA',             46.22764,  2.21375),
  ('CA','Canada','Americas',         56.13037,-106.34677),
  ('AU','Australia','APAC',         -25.27440,133.77514),
  ('IN','India','APAC',              20.59368, 78.96288),
  ('JP','Japan','APAC',              36.20482,138.25292),
  ('BR','Brazil','Americas',        -14.23500,-51.92528),
  ('NL','Netherlands','EMEA',        52.13263,  5.29127),
  ('ES','Spain','EMEA',              40.46367, -3.74922),
  ('IT','Italy','EMEA',              41.87194, 12.56738),
  ('SE','Sweden','EMEA',             60.12816, 18.64351),
  ('NO','Norway','EMEA',             60.47202,  8.46895),
  ('DK','Denmark','EMEA',            56.26392,  9.50179),
  ('FI','Finland','EMEA',            61.92411, 25.74815),
  ('IE','Ireland','EMEA',            53.41291, -8.24389),
  ('CH','Switzerland','EMEA',        46.81819,  8.22751),
  ('AT','Austria','EMEA',            47.51623, 14.55007),
  ('BE','Belgium','EMEA',            50.50389,  4.46994),
  ('PL','Poland','EMEA',             51.91944, 19.14514),
  ('PT','Portugal','EMEA',           39.39987, -8.22445),
  ('MX','Mexico','Americas',         23.63450,-102.55278),
  ('AR','Argentina','Americas',     -38.41610,-63.61667),
  ('CL','Chile','Americas',         -35.67515,-71.54297),
  ('CO','Colombia','Americas',        4.57087,-74.29733),
  ('SG','Singapore','APAC',           1.35208,103.81984),
  ('KR','South Korea','APAC',        35.90776,127.76692),
  ('CN','China','APAC',              35.86166,104.19540),
  ('HK','Hong Kong','APAC',          22.39643,114.10950),
  ('NZ','New Zealand','APAC',       -40.90056,174.88597),
  ('ZA','South Africa','EMEA',      -30.55948, 22.93751),
  ('AE','United Arab Emirates','EMEA',23.42408,53.84782),
  ('IL','Israel','EMEA',             31.04605, 34.85161),
  ('TR','Turkey','EMEA',             38.96375, 35.24332),
  ('SA','Saudi Arabia','EMEA',       23.88594, 45.07916),
  ('ID','Indonesia','APAC',          -0.78928,113.92133),
  ('MY','Malaysia','APAC',            4.21048,101.97577),
  ('TH','Thailand','APAC',           15.87003,100.99254),
  ('PH','Philippines','APAC',        12.87972,121.77402);

-- ════════════════════════════ PLANS (5) ══════════════════════════
INSERT INTO plans (name, tier, monthly_price_cents, annual_price_cents, seats_included, features) VALUES
  ('Free',       'free',            0,        0,    1, ARRAY['dashboards']),
  ('Starter',    'starter',      2900,    29000,    3, ARRAY['dashboards','exports']),
  ('Growth',     'growth',       9900,    99000,   10, ARRAY['dashboards','exports','api','alerts']),
  ('Business',   'business',    29900,   299000,   25, ARRAY['dashboards','exports','api','alerts','sso']),
  ('Enterprise', 'enterprise',  99900,   999000,  100, ARRAY['dashboards','exports','api','alerts','sso','audit_log','sla']);

-- ════════════════════════════ ACCOUNTS (3000) ════════════════════
-- created_at: base date + a month offset chosen with a Q4 skew. We
-- map g to a month-in-window via a weighted modulo so Oct/Nov/Dec of
-- each year are over-represented. The skew weights are encoded in a
-- 24-element array: months 4,5,16,17 (≈ Q4-ish of each year window)
-- get repeated. Country: ~35% US, ~15% GB, the rest spread.
INSERT INTO accounts (name, slug, country_id, industry, employee_count, signup_channel, signup_ip, metadata, tags, created_at)
SELECT
  'Account ' || lpad(g::text, 4, '0'),
  'acct-' || lpad(g::text, 4, '0'),
  -- US-heavy: g%100 < 35 → US, < 50 → GB, else spread across 3..40
  CASE
    WHEN (g % 100) < 35 THEN 1
    WHEN (g % 100) < 50 THEN 2
    ELSE 3 + (g % 38)
  END,
  (ARRAY['SaaS','Fintech','E-commerce','Healthcare','Education','Media','Gaming',
         'Logistics','Manufacturing','Real Estate','Travel','Energy'])[1 + (g % 12)],
  (ARRAY[5,12,25,50,120,300,800,2000])[1 + (g % 8)],
  (ARRAY['organic','paid_search','social','referral','partner','outbound']::signup_channel[])[1 + (g % 6)],
  ('203.0.113.' || (g % 255))::inet,
  jsonb_build_object(
    'plan_hint', (ARRAY['free','starter','growth','business','enterprise'])[1 + (g % 5)],
    'trial', (g % 3 = 0),
    'score', round((random()*100)::numeric, 1)
  ),
  -- text[] tags
  CASE WHEN g % 4 = 0 THEN ARRAY['priority','vip']
       WHEN g % 4 = 1 THEN ARRAY['smb']
       WHEN g % 4 = 2 THEN ARRAY['mid-market','expansion']
       ELSE ARRAY['enterprise','strategic'] END,
  -- created_at with Q4 skew. month_idx 0..23 across the 24-mo window;
  -- the weighting array repeats Oct/Nov/Dec slots to over-sample them.
  TIMESTAMPTZ '2023-06-01 00:00:00+00'
    + (ARRAY[0,1,2,3,4,4,5,6,6,7,7,8,9,10,11,12,13,14,15,15,16,16,17,17,18,19,20,21,21,22,22,23,23,23]
        )[1 + (g % 34)] * INTERVAL '1 month'
    + (random() * 27)::int * INTERVAL '1 day'
    + (random() * 86400)::int * INTERVAL '1 second'
FROM generate_series(1, 3000) AS g;

-- ════════════════════════════ USERS (12000) ══════════════════════
-- 4 users per account on average. account_id = (g % 3000) + 1.
-- created_at >= the account's created_at is approximated by adding a
-- small positive offset to a per-account base; to keep it set-based
-- we just join to the account and add a random non-negative offset.
INSERT INTO users (account_id, email, full_name, role, is_active, last_seen_at, created_at)
SELECT
  a.id,
  ('user' || g || '@' || a.slug || '.example.com')::citext,
  (ARRAY['Alex','Sam','Jordan','Taylor','Morgan','Casey','Riley','Jamie','Avery','Quinn',
         'Drew','Reese','Skyler','Cameron','Dakota','Emerson'])[1 + (g % 16)]
    || ' ' ||
  (ARRAY['Smith','Johnson','Lee','Brown','Garcia','Miller','Davis','Wilson','Moore','Taylor',
         'Anderson','Thomas','Jackson','White','Harris','Martin'])[1 + ((g / 16) % 16)],
  (ARRAY['owner','admin','member','member','member','viewer']::user_role[])[1 + (g % 6)],
  (g % 11 <> 0),                                  -- ~9% inactive
  a.created_at + (random() * 700)::int * INTERVAL '1 day',
  a.created_at + (g % 90) * INTERVAL '1 day'
FROM generate_series(1, 12000) AS g
JOIN accounts a ON a.id = (g % 3000) + 1;

-- ════════════════════════════ SUBSCRIPTIONS (3500) ═══════════════
-- One+ per account. plan_id 1..5 chosen by g%5. The 'starter' tier
-- (plan_id 2) is the high-churn one: it cancels far more often. mrr
-- is the plan's monthly price (annual normalized to /12), scaled by a
-- small seat factor. started_at follows the account's created_at.
INSERT INTO subscriptions (account_id, plan_id, status, billing_interval, mrr_cents, seats, trial_length, started_at, canceled_at, created_at)
SELECT
  a.id,
  p.id,
  -- status: starter (plan 2) churns ~45%, others ~12%. A slice trial/paused/past_due.
  CASE
    WHEN p.id = 2 AND (g % 100) < 45 THEN 'canceled'::subscription_status
    WHEN p.id <> 2 AND (g % 100) < 12 THEN 'canceled'::subscription_status
    WHEN (g % 100) < 50 THEN 'active'::subscription_status            -- (only reached when not canceled above)
    WHEN (g % 100) < 56 THEN 'trialing'::subscription_status
    WHEN (g % 100) < 60 THEN 'past_due'::subscription_status
    WHEN (g % 100) < 63 THEN 'paused'::subscription_status
    ELSE 'active'::subscription_status
  END,
  bi.bi,
  -- mrr: monthly price (or annual/12) scaled by seats factor 1.0..1.5
  CASE WHEN bi.bi = 'monthly'
       THEN p.monthly_price_cents
       ELSE (p.annual_price_cents / 12) END
    + (p.monthly_price_cents * (g % 5) / 10),     -- small seat-based bump
  p.seats_included + (g % 5),
  CASE WHEN g % 3 = 0 THEN INTERVAL '14 days' ELSE INTERVAL '0 days' END,
  sub.started_at,
  -- canceled_at: strictly after started_at, only for the canceled slice
  CASE
    WHEN (p.id = 2 AND (g % 100) < 45) OR (p.id <> 2 AND (g % 100) < 12)
    THEN sub.started_at + (30 + (random() * 400)::int) * INTERVAL '1 day'
    ELSE NULL
  END,
  sub.started_at
FROM generate_series(1, 3500) AS g
JOIN accounts a ON a.id = (g % 3000) + 1
JOIN plans p ON p.id = 1 + (g % 5)
CROSS JOIN LATERAL (SELECT (ARRAY['monthly','annual']::billing_interval[])[1 + (g % 2)] AS bi) bi
CROSS JOIN LATERAL (
  SELECT GREATEST(
           a.created_at + (g % 30) * INTERVAL '1 day',
           TIMESTAMPTZ '2023-06-01 00:00:00+00'
         ) AS started_at
) sub;

-- Fix up: clamp canceled_at into [started_at + 1 day, window end].
-- Capping at the window end alone could land it before started_at for
-- subs that started in the final month, so we floor at started_at too.
UPDATE subscriptions
   SET canceled_at = GREATEST(
         started_at + INTERVAL '1 day',
         LEAST(canceled_at, TIMESTAMPTZ '2025-05-31 23:59:59+00')
       )
 WHERE canceled_at IS NOT NULL;

-- ════════════════════════════ SUBSCRIPTION_ITEMS (6000) ══════════
-- ~1.7 items per subscription. subscription_id = (g % 3500) + 1.
INSERT INTO subscription_items (subscription_id, product, quantity, unit_price_cents, created_at)
SELECT
  s.id,
  (ARRAY['base','seats','storage','support','sso'])[1 + (g % 5)],
  1 + (g % 10),
  (ARRAY[0, 1500, 500, 9900, 19900])[1 + (g % 5)],
  s.started_at
FROM generate_series(1, 6000) AS g
JOIN subscriptions s ON s.id = (g % 3500) + 1;

-- ════════════════════════════ INVOICES (40000) ══════════════════
-- ~11 invoices per account on average. issued_at spread monthly
-- across the window. status mostly paid, some open/void/uncollectible.
INSERT INTO invoices (account_id, subscription_id, status, amount_cents, tax_cents, currency, issued_at, due_at, paid_at, metadata)
SELECT
  a.id,
  s.id,
  iv.status,
  iv.amount_cents,
  (iv.amount_cents * 8 / 100),                    -- ~8% tax
  'usd',
  iv.issued_at,
  iv.issued_at + INTERVAL '14 days',
  CASE WHEN iv.status = 'paid'
       THEN iv.issued_at + (1 + (random() * 20)::int) * INTERVAL '1 day'
       ELSE NULL END,
  jsonb_build_object('period', to_char(iv.issued_at, 'YYYY-MM'), 'auto', true)
FROM generate_series(1, 40000) AS g
JOIN accounts a ON a.id = (g % 3000) + 1
JOIN subscriptions s ON s.id = (g % 3500) + 1
CROSS JOIN LATERAL (
  SELECT
    -- status mix: ~82% paid, 8% open, 4% draft, 3% void, 3% uncollectible
    CASE
      WHEN (g % 100) < 82 THEN 'paid'::invoice_status
      WHEN (g % 100) < 90 THEN 'open'::invoice_status
      WHEN (g % 100) < 94 THEN 'draft'::invoice_status
      WHEN (g % 100) < 97 THEN 'void'::invoice_status
      ELSE 'uncollectible'::invoice_status
    END AS status,
    (2900 + (g % 30) * 1000 + (random() * 5000)::int)::bigint AS amount_cents,
    -- issued_at spread across the 24-month window
    TIMESTAMPTZ '2023-06-01 00:00:00+00'
      + (g % 24) * INTERVAL '1 month'
      + (random() * 27)::int * INTERVAL '1 day' AS issued_at
) iv;

-- ════════════════════════════ INVOICE_LINE_ITEMS (70000) ═════════
-- ~1.75 lines per invoice. invoice_id = (g % 40000) + 1.
INSERT INTO invoice_line_items (invoice_id, description, quantity, unit_price_cents, amount_cents)
SELECT
  i.id,
  (ARRAY['Subscription','Overage','Seats','Storage','Support','Setup fee'])[1 + (g % 6)],
  li.qty,
  li.unit,
  li.qty * li.unit
FROM generate_series(1, 70000) AS g
JOIN invoices i ON i.id = (g % 40000) + 1
CROSS JOIN LATERAL (
  SELECT (1 + (g % 8)) AS qty,
         (ARRAY[2900, 500, 1500, 200, 9900, 19900])[1 + (g % 6)]::bigint AS unit
) li;

-- ════════════════════════════ PAYMENTS (40000) ══════════════════
-- One payment per invoice (invoice_id = g for g in 1..40000). status
-- tracks the invoice loosely: paid invoices → succeeded, others mixed.
INSERT INTO payments (invoice_id, account_id, status, method, amount_cents, created_at)
SELECT
  i.id,
  i.account_id,
  CASE
    WHEN i.status = 'paid' THEN 'succeeded'::payment_status
    WHEN i.status = 'uncollectible' THEN 'failed'::payment_status
    WHEN (g % 20) = 0 THEN 'refunded'::payment_status
    WHEN i.status = 'open' THEN 'pending'::payment_status
    ELSE 'failed'::payment_status
  END,
  (ARRAY['card','card','card','ach','wire','paypal']::payment_method[])[1 + (g % 6)],
  i.amount_cents + i.tax_cents,
  COALESCE(i.paid_at, i.issued_at + INTERVAL '2 days')
FROM generate_series(1, 40000) AS g
JOIN invoices i ON i.id = g;

-- ════════════════════════════ FEATURE_EVENTS (250000) ════════════
-- HIGH VOLUME. user_id = (g % 12000) + 1; account_id derived from the
-- user. Power-law feature distribution: a few features dominate. ts
-- spread across the window, weighted slightly toward recent months.
INSERT INTO feature_events (user_id, account_id, kind, feature, props, ts)
SELECT
  u.id,
  u.account_id,
  (ARRAY['page_view','feature_use','feature_use','api_call','export','invite','error']::event_kind[])[1 + (g % 7)],
  -- power-law: features earlier in the list appear far more often.
  -- (g % 50) buckets: 0..24 → 'dashboard' (50%), 25..36 → 'reports',
  -- 37..43 → 'charts', 44..46 → 'export', 47..48 → 'alerts', 49 → 'api'.
  CASE
    WHEN (g % 50) < 25 THEN 'dashboard'
    WHEN (g % 50) < 37 THEN 'reports'
    WHEN (g % 50) < 44 THEN 'charts'
    WHEN (g % 50) < 47 THEN 'export'
    WHEN (g % 50) < 49 THEN 'alerts'
    ELSE 'api'
  END,
  jsonb_build_object(
    'duration_ms', (random() * 5000)::int,
    'success', (g % 13 <> 0),
    'source', (ARRAY['web','mobile','api'])[1 + (g % 3)]
  ),
  -- ts: bias toward the back half of the window via two overlapping ranges
  CASE WHEN (g % 3) = 0
    THEN TIMESTAMPTZ '2024-06-01 00:00:00+00' + (random() * 364) * INTERVAL '1 day'
    ELSE TIMESTAMPTZ '2023-06-01 00:00:00+00' + (random() * 729) * INTERVAL '1 day'
  END
FROM generate_series(1, fab_seed_int('saas_feature_events', 250000)) AS g
JOIN users u ON u.id = (g % 12000) + 1;

-- ════════════════════════════ SESSIONS (60000) ══════════════════
-- duration_seconds varies by device: desktop longer, mobile shorter,
-- so a box plot grouped by device/plan tier shows real spread.
INSERT INTO sessions (user_id, account_id, device_type, country_id, duration_seconds, page_count, started_at)
SELECT
  u.id,
  u.account_id,
  dt.device,
  a.country_id,
  -- base duration by device + noise, floored at 5s
  GREATEST(5,
    (CASE dt.device WHEN 'desktop' THEN 600 WHEN 'tablet' THEN 300 ELSE 180 END)
    + (random() * (CASE dt.device WHEN 'desktop' THEN 1800 WHEN 'tablet' THEN 700 ELSE 400 END))::int
  ),
  1 + (random() * 40)::int,
  TIMESTAMPTZ '2023-06-01 00:00:00+00' + (random() * 729) * INTERVAL '1 day'
                                       + (random() * 86400)::int * INTERVAL '1 second'
FROM generate_series(1, fab_seed_int('saas_sessions', 60000)) AS g
JOIN users u ON u.id = (g % 12000) + 1
JOIN accounts a ON a.id = u.account_id
CROSS JOIN LATERAL (
  SELECT (ARRAY['desktop','desktop','mobile','mobile','mobile','tablet']::device_type[])[1 + (g % 6)] AS device
) dt;

-- ════════════════════════════ SUPPORT_TICKETS (8000) ═════════════
INSERT INTO support_tickets (account_id, priority, status, subject, csat, opened_at, resolved_at)
SELECT
  a.id,
  (ARRAY['low','normal','normal','high','urgent']::ticket_priority[])[1 + (g % 5)],
  st.status,
  (ARRAY['Login issue','Billing question','API error','Feature request',
         'Data export failing','Performance slow','Integration help','Bug report'])[1 + (g % 8)],
  CASE WHEN st.status IN ('resolved','closed') THEN 1 + (g % 5) ELSE NULL END,
  st.opened_at,
  CASE WHEN st.status IN ('resolved','closed')
       THEN st.opened_at + (1 + (random() * 120)::int) * INTERVAL '1 hour'
       ELSE NULL END
FROM generate_series(1, fab_seed_int('saas_support_tickets', 8000)) AS g
JOIN accounts a ON a.id = (g % 3000) + 1
CROSS JOIN LATERAL (
  SELECT
    CASE WHEN (g % 100) < 60 THEN 'closed'::ticket_status
         WHEN (g % 100) < 80 THEN 'resolved'::ticket_status
         WHEN (g % 100) < 92 THEN 'pending'::ticket_status
         ELSE 'open'::ticket_status END AS status,
    TIMESTAMPTZ '2023-06-01 00:00:00+00' + (random() * 729) * INTERVAL '1 day' AS opened_at
) st;

-- ════════════════════════════ NPS_RESPONSES (5000) ═══════════════
-- Score 0..10. Deliberate DIP in 2024-09: that month is detractor-
-- heavy (scores skew low). Otherwise promoter-leaning.
INSERT INTO nps_responses (account_id, user_id, score, comment, submitted_at)
SELECT
  u.account_id,
  u.id,
  CASE
    -- the 2024-09 dip slice: submitted in Sep 2024 → low scores
    WHEN nps.submitted_at >= TIMESTAMPTZ '2024-09-01 00:00:00+00'
     AND nps.submitted_at <  TIMESTAMPTZ '2024-10-01 00:00:00+00'
      THEN (random() * 5)::int                    -- 0..5 (detractors/passives)
    -- otherwise: promoter-leaning 6..10 (more 9/10)
    ELSE 6 + (random() * 4)::int
  END,
  CASE WHEN g % 5 = 0 THEN (ARRAY['Love it','Could be faster','Great support','Missing features','Will recommend'])[1 + ((g / 5) % 5)] ELSE NULL END,
  nps.submitted_at
FROM generate_series(1, fab_seed_int('saas_nps_responses', 5000)) AS g
JOIN users u ON u.id = (g % 12000) + 1
CROSS JOIN LATERAL (
  -- weight ~12% of responses into Sep 2024 so the dip is visible
  SELECT CASE WHEN (g % 100) < 12
              THEN TIMESTAMPTZ '2024-09-01 00:00:00+00' + (random() * 29) * INTERVAL '1 day'
              ELSE TIMESTAMPTZ '2023-06-01 00:00:00+00' + (random() * 729) * INTERVAL '1 day'
         END AS submitted_at
) nps;

-- ════════════════════════════ PLAN_CHANGES (2500) ════════════════
-- from_plan / to_plan are distinct plan ids. Bias toward upgrades
-- (to > from) so the sankey shows net expansion, with a downgrade tail.
INSERT INTO plan_changes (account_id, from_plan_id, to_plan_id, mrr_delta_cents, changed_at)
SELECT
  a.id,
  pc.from_id,
  pc.to_id,
  -- delta = (to monthly - from monthly)
  (pt.monthly_price_cents - pf.monthly_price_cents),
  TIMESTAMPTZ '2023-07-01 00:00:00+00' + (random() * 698) * INTERVAL '1 day'
FROM generate_series(1, fab_seed_int('saas_plan_changes', 2500)) AS g
JOIN accounts a ON a.id = (g % 3000) + 1
CROSS JOIN LATERAL (
  SELECT
    (1 + (g % 4))::int AS from_id,                -- 1..4
    -- 70% upgrade (to = from+1), 30% downgrade (to = from-1, floored at 1)
    CASE WHEN (g % 10) < 7
         THEN LEAST(5, (1 + (g % 4)) + 1)
         ELSE GREATEST(1, (1 + (g % 4)) - 1) END AS to_id
) pc
JOIN plans pf ON pf.id = pc.from_id
JOIN plans pt ON pt.id = pc.to_id
WHERE pc.from_id <> pc.to_id;

-- ════════════════════════════ ONBOARDING_EVENTS (~15000) ═════════
-- 6 ordered steps for the first 2500 accounts → 15000 rows. Funnel
-- drop-off: a step is only emitted as completed if every earlier step
-- for that account was also completed (monotone narrowing), so
-- v_onboarding_funnel is a clean shrinking funnel. The per-account
-- "reach depth" is drawn once (1..6) and weighted toward deeper steps.
INSERT INTO onboarding_events (account_id, step_order, step_name, completed, ts)
SELECT
  a.id,
  step.ord,
  (ARRAY['signup','verify_email','create_workspace','invite_team','connect_data','first_dashboard'])[step.ord],
  -- completed iff this step <= the account's reached depth
  (step.ord <= depth.d),
  a.created_at + step.ord * INTERVAL '1 day' + (random() * 12)::int * INTERVAL '1 hour'
FROM accounts a
JOIN generate_series(1, 6) AS step(ord) ON true
CROSS JOIN LATERAL (
  -- depth in 1..6, biased so most accounts complete several steps but
  -- fewer reach the end → monotone funnel. uses account id for spread.
  SELECT (CASE
    WHEN (a.id % 100) < 10 THEN 1
    WHEN (a.id % 100) < 25 THEN 2
    WHEN (a.id % 100) < 45 THEN 3
    WHEN (a.id % 100) < 65 THEN 4
    WHEN (a.id % 100) < 85 THEN 5
    ELSE 6 END)::int AS d
) depth
WHERE a.id <= 2500;

ANALYZE;
