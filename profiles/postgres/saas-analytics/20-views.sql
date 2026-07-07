-- Analytics views. The visualizer reads ONE relation at a time and
-- only does GROUP BY + count/sum/avg/min/max/median/percentile +
-- date_trunc + WHERE + bin. So every join / window / CTE / cohort
-- lives HERE, and each view exposes flat dimension + measure columns.
--
-- All money columns stay in cents (BIGINT/NUMERIC). Divide by 100 in
-- the chart layer. NULLIF guards every division so we never divide by
-- zero.

-- 1) v_subscriptions_enriched ─────────────────────────────────────
--    Row per subscription, flattened with plan + account + country.
--    Source for bar / row / pie / map / combo / table / pivot.
CREATE OR REPLACE VIEW v_subscriptions_enriched AS
SELECT
  s.id                AS subscription_id,
  s.account_id,
  a.name              AS account_name,
  a.industry,
  a.signup_channel,
  p.name              AS plan_name,
  p.tier              AS plan_tier,
  s.billing_interval,
  s.status,
  c.iso_code          AS country_code,
  c.name              AS country_name,
  c.region,
  s.seats,
  s.mrr_cents,
  s.trial_length,
  s.started_at,
  s.canceled_at,
  (s.canceled_at IS NOT NULL) AS is_churned
FROM subscriptions s
JOIN plans p     ON p.id = s.plan_id
JOIN accounts a  ON a.id = s.account_id
JOIN countries c ON c.id = a.country_id;

-- 2) v_mrr_daily ──────────────────────────────────────────────────
--    One row per calendar day across the data window with the total
--    active MRR that day, active subscription count, and how many
--    subscriptions newly started that day.
--    A subscription is active on day D iff started_at::date <= D AND
--    (canceled_at IS NULL OR canceled_at::date > D).
--    Source for line / area / combo / trend.
CREATE OR REPLACE VIEW v_mrr_daily AS
WITH bounds AS (
  -- Anchor the series to the DATA's own range (not now()): the seed is
  -- pinned to fixed 2023–2025 literals, so a now()-based upper bound
  -- would tack on a long flat tail once the wall clock drifts past it.
  SELECT date_trunc('day', min(started_at))::date AS d0,
         date_trunc('day', GREATEST(max(started_at), max(canceled_at)))::date AS d1
    FROM subscriptions
),
days AS (
  SELECT generate_series(b.d0, b.d1, INTERVAL '1 day')::date AS day
    FROM bounds b
)
SELECT
  d.day,
  COALESCE(SUM(s.mrr_cents) FILTER (
    WHERE s.started_at::date <= d.day
      AND (s.canceled_at IS NULL OR s.canceled_at::date > d.day)
      AND s.status <> 'trialing'
  ), 0)::bigint AS active_mrr_cents,
  COUNT(*) FILTER (
    WHERE s.started_at::date <= d.day
      AND (s.canceled_at IS NULL OR s.canceled_at::date > d.day)
      AND s.status <> 'trialing'
  )::bigint AS active_subscriptions,
  COUNT(*) FILTER (WHERE s.started_at::date = d.day)::bigint AS new_subscriptions
FROM days d
LEFT JOIN subscriptions s ON true
GROUP BY d.day
ORDER BY d.day;

-- 3) v_revenue_by_plan_month ──────────────────────────────────────
--    month × plan_name × paid-invoice revenue × invoice count.
--    Revenue counts only paid invoices, attributed to the invoice's
--    subscription's plan. Source for combo / stacked / normalized bar
--    / pivot.
CREATE OR REPLACE VIEW v_revenue_by_plan_month AS
SELECT
  date_trunc('month', i.issued_at)::date AS month,
  p.name                                 AS plan_name,
  p.tier                                 AS plan_tier,
  COALESCE(SUM(i.amount_cents), 0)::bigint AS revenue_cents,
  COUNT(*)::bigint                       AS invoice_count
FROM invoices i
JOIN subscriptions s ON s.id = i.subscription_id
JOIN plans p         ON p.id = s.plan_id
WHERE i.status = 'paid'
GROUP BY 1, p.name, p.tier
ORDER BY 1, p.name;

-- 4) v_signups_by_country ─────────────────────────────────────────
--    Per country: total signups, paid accounts, conversion %. lat/lng
--    carried for the map layer. Source for map / bar / row / table.
CREATE OR REPLACE VIEW v_signups_by_country AS
WITH paid AS (
  -- accounts with at least one non-free, non-canceled subscription
  SELECT DISTINCT s.account_id
    FROM subscriptions s
    JOIN plans p ON p.id = s.plan_id
   WHERE p.tier <> 'free'
     AND s.status IN ('active', 'past_due', 'paused')
)
SELECT
  c.iso_code        AS country_code,
  c.name            AS country_name,
  c.region,
  c.lat,
  c.lng,
  COUNT(a.id)::bigint AS signups,
  COUNT(a.id) FILTER (WHERE pa.account_id IS NOT NULL)::bigint AS paid_accounts,
  ROUND(
    100.0 * COUNT(a.id) FILTER (WHERE pa.account_id IS NOT NULL)
          / NULLIF(COUNT(a.id), 0)
  , 1) AS paid_conversion_pct
FROM countries c
LEFT JOIN accounts a ON a.country_id = c.id
LEFT JOIN paid pa    ON pa.account_id = a.id
GROUP BY c.id, c.iso_code, c.name, c.region, c.lat, c.lng
ORDER BY signups DESC;

-- 5) v_onboarding_funnel ──────────────────────────────────────────
--    step_order, step_name, accounts that completed that step.
--    Ordered by step_order, monotonically narrowing. Source: funnel.
CREATE OR REPLACE VIEW v_onboarding_funnel AS
SELECT
  step_order,
  MIN(step_name)                                       AS step_name,
  COUNT(*) FILTER (WHERE completed)::bigint            AS accounts_reached
FROM onboarding_events
GROUP BY step_order
ORDER BY step_order;

-- 6) v_plan_flows ─────────────────────────────────────────────────
--    from_plan → to_plan with the count of accounts that made that
--    move. Source: sankey (also a heat/pivot of upgrade vs downgrade).
CREATE OR REPLACE VIEW v_plan_flows AS
SELECT
  pf.name AS from_plan,
  pt.name AS to_plan,
  pf.tier AS from_tier,
  pt.tier AS to_tier,
  COUNT(*)::bigint AS accounts,
  SUM(pc.mrr_delta_cents)::bigint AS net_mrr_delta_cents
FROM plan_changes pc
JOIN plans pf ON pf.id = pc.from_plan_id
JOIN plans pt ON pt.id = pc.to_plan_id
GROUP BY pf.name, pt.name, pf.tier, pt.tier
ORDER BY accounts DESC;

-- 7) v_session_facts ──────────────────────────────────────────────
--    One row per session, with the account's plan tier as the
--    "segment" dimension. Source for box plot (percentiles grouped by
--    segment), histogram (bin duration_seconds), scatter/bubble.
CREATE OR REPLACE VIEW v_session_facts AS
SELECT
  se.id              AS session_id,
  COALESCE(pt.tier, 'free') AS segment,        -- plan tier of the account
  c.iso_code         AS country_code,
  c.region,
  se.device_type,
  se.duration_seconds::numeric AS duration_seconds,
  se.page_count,
  se.started_at
FROM sessions se
JOIN countries c ON c.id = se.country_id
LEFT JOIN LATERAL (
  -- the account's "current" plan tier: the most recently started
  -- non-canceled subscription, else free.
  SELECT p.tier
    FROM subscriptions s
    JOIN plans p ON p.id = s.plan_id
   WHERE s.account_id = se.account_id
     AND s.status <> 'canceled'
   ORDER BY s.started_at DESC
   LIMIT 1
) pt ON true;

-- 8) v_feature_usage_daily ────────────────────────────────────────
--    day × feature × distinct active users × total events.
--    Source for trend / line / area / stacked area.
CREATE OR REPLACE VIEW v_feature_usage_daily AS
SELECT
  date_trunc('day', ts)::date AS day,
  feature,
  COUNT(DISTINCT user_id)::bigint AS active_users,
  COUNT(*)::bigint                AS events
FROM feature_events
GROUP BY 1, feature
ORDER BY 1, feature;

-- 9) v_nps_summary ────────────────────────────────────────────────
--    Single-row headline NPS: %promoters - %detractors, plus the
--    response count. Source for gauge / progress / kpi.
CREATE OR REPLACE VIEW v_nps_summary AS
SELECT
  ROUND(
    100.0 * COUNT(*) FILTER (WHERE score >= 9) / NULLIF(COUNT(*), 0)
  - 100.0 * COUNT(*) FILTER (WHERE score <= 6) / NULLIF(COUNT(*), 0)
  , 1) AS nps_score,
  COUNT(*)::bigint AS responses,
  COUNT(*) FILTER (WHERE score >= 9)::bigint AS promoters,
  COUNT(*) FILTER (WHERE score BETWEEN 7 AND 8)::bigint AS passives,
  COUNT(*) FILTER (WHERE score <= 6)::bigint AS detractors,
  ROUND(AVG(score)::numeric, 2) AS avg_score
FROM nps_responses;

--    Bonus: NPS by month (so the seeded 2024-09 dip is chartable on a
--    line). Same %promoter - %detractor formula, grouped by month.
CREATE OR REPLACE VIEW v_nps_monthly AS
SELECT
  date_trunc('month', submitted_at)::date AS month,
  ROUND(
    100.0 * COUNT(*) FILTER (WHERE score >= 9) / NULLIF(COUNT(*), 0)
  - 100.0 * COUNT(*) FILTER (WHERE score <= 6) / NULLIF(COUNT(*), 0)
  , 1) AS nps_score,
  COUNT(*)::bigint AS responses
FROM nps_responses
GROUP BY 1
ORDER BY 1;

-- 10) v_cohort_retention ──────────────────────────────────────────
--     Accounts grouped by signup-month cohort; for each cohort, how
--     many accounts were still "active" (had at least one feature
--     event) N months after signup, and the retention %. Source for
--     pivot / heat.
CREATE OR REPLACE VIEW v_cohort_retention AS
WITH cohorts AS (
  SELECT a.id AS account_id,
         date_trunc('month', a.created_at)::date AS cohort_month
    FROM accounts a
),
cohort_size AS (
  SELECT cohort_month, COUNT(*)::bigint AS n
    FROM cohorts GROUP BY cohort_month
),
activity AS (
  -- distinct (account, months_since) pairs the account was active in.
  -- months_since computed as whole calendar months between two
  -- month-truncated dates (avoids age()'s type-resolution quirks).
  SELECT DISTINCT
         co.account_id,
         co.cohort_month,
         ( (EXTRACT(YEAR  FROM date_trunc('month', fe.ts)::date) - EXTRACT(YEAR  FROM co.cohort_month)) * 12
         + (EXTRACT(MONTH FROM date_trunc('month', fe.ts)::date) - EXTRACT(MONTH FROM co.cohort_month)) )::int AS months_since
    FROM cohorts co
    JOIN feature_events fe ON fe.account_id = co.account_id
)
SELECT
  ac.cohort_month,
  ac.months_since,
  COUNT(DISTINCT ac.account_id)::bigint AS retained_accounts,
  ROUND(
    100.0 * COUNT(DISTINCT ac.account_id) / NULLIF(cs.n, 0)
  , 1) AS retention_pct
FROM activity ac
JOIN cohort_size cs ON cs.cohort_month = ac.cohort_month
WHERE ac.months_since >= 0
GROUP BY ac.cohort_month, ac.months_since, cs.n
ORDER BY ac.cohort_month, ac.months_since;

-- 11) v_mrr_change_monthly ────────────────────────────────────────
--     Signed net MRR change per month: (MRR added by subs that
--     started that month) - (MRR lost by subs canceled that month).
--     A running total of net_change_cents reproduces the MRR curve →
--     waterfall source.
CREATE OR REPLACE VIEW v_mrr_change_monthly AS
WITH starts AS (
  SELECT date_trunc('month', started_at)::date AS month,
         SUM(mrr_cents)::bigint AS added_cents
    FROM subscriptions
   WHERE status <> 'trialing'
   GROUP BY 1
),
ends AS (
  SELECT date_trunc('month', canceled_at)::date AS month,
         SUM(mrr_cents)::bigint AS lost_cents
    FROM subscriptions
   WHERE canceled_at IS NOT NULL
   GROUP BY 1
),
months AS (
  SELECT month FROM starts
  UNION
  SELECT month FROM ends
)
SELECT
  m.month,
  COALESCE(s.added_cents, 0)                          AS added_cents,
  COALESCE(e.lost_cents, 0)                           AS lost_cents,
  (COALESCE(s.added_cents, 0) - COALESCE(e.lost_cents, 0))::bigint AS net_change_cents,
  SUM(COALESCE(s.added_cents, 0) - COALESCE(e.lost_cents, 0))
    OVER (ORDER BY m.month)::bigint                   AS running_mrr_cents
FROM months m
LEFT JOIN starts s ON s.month = m.month
LEFT JOIN ends   e ON e.month = m.month
ORDER BY m.month;

-- 12) v_arr_bridge ────────────────────────────────────────────────
--     The classic ARR/MRR bridge for the trailing 12 months as 6
--     ordered, signed steps (Starting, New, Expansion, Contraction,
--     Churn, Ending). Source: waterfall.
CREATE OR REPLACE VIEW v_arr_bridge AS
WITH win AS (
  -- Trailing 12 months OF DATA. Anchoring on the latest subscription
  -- start (not now()) keeps the bridge populated regardless of when the
  -- container boots — the seed is pinned to fixed 2023–2025 dates.
  SELECT (date_trunc('month', mx) - INTERVAL '11 months')::date AS t0,
         (date_trunc('month', mx) + INTERVAL '1 month')::date   AS t1
    FROM (SELECT max(started_at) AS mx FROM subscriptions) s
),
-- Starting MRR = active MRR as of t0 (subs started before t0 and not
-- yet canceled at t0).
starting AS (
  SELECT COALESCE(SUM(s.mrr_cents), 0)::bigint AS amt
    FROM subscriptions s, win w
   WHERE s.status <> 'trialing'
     AND s.started_at::date < w.t0
     AND (s.canceled_at IS NULL OR s.canceled_at::date >= w.t0)
),
-- New = MRR from subs that started within the window.
newbiz AS (
  SELECT COALESCE(SUM(s.mrr_cents), 0)::bigint AS amt
    FROM subscriptions s, win w
   WHERE s.status <> 'trialing'
     AND s.started_at::date >= w.t0
     AND s.started_at::date <  w.t1
),
-- Expansion / Contraction from plan_changes within the window.
expansion AS (
  SELECT COALESCE(SUM(mrr_delta_cents), 0)::bigint AS amt
    FROM plan_changes pc, win w
   WHERE pc.mrr_delta_cents > 0
     AND pc.changed_at::date >= w.t0 AND pc.changed_at::date < w.t1
),
contraction AS (
  SELECT COALESCE(SUM(mrr_delta_cents), 0)::bigint AS amt
    FROM plan_changes pc, win w
   WHERE pc.mrr_delta_cents < 0
     AND pc.changed_at::date >= w.t0 AND pc.changed_at::date < w.t1
),
-- Churn = MRR lost to cancellations within the window (negative).
churn AS (
  SELECT (-COALESCE(SUM(s.mrr_cents), 0))::bigint AS amt
    FROM subscriptions s, win w
   WHERE s.canceled_at IS NOT NULL
     AND s.canceled_at::date >= w.t0 AND s.canceled_at::date < w.t1
)
SELECT 1 AS step_order, 'Starting MRR' AS step, (SELECT amt FROM starting) AS amount_cents
UNION ALL SELECT 2, 'New',         (SELECT amt FROM newbiz)
UNION ALL SELECT 3, 'Expansion',   (SELECT amt FROM expansion)
UNION ALL SELECT 4, 'Contraction', (SELECT amt FROM contraction)
UNION ALL SELECT 5, 'Churn',       (SELECT amt FROM churn)
UNION ALL SELECT 6, 'Ending MRR',
  (SELECT amt FROM starting) + (SELECT amt FROM newbiz)
+ (SELECT amt FROM expansion) + (SELECT amt FROM contraction)
+ (SELECT amt FROM churn)
ORDER BY step_order;
