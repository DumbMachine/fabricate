-- Wheelio (rideshare example) core schema.
-- The story: payments-webhook died 2026-07-03 14:05 UTC, so payment
-- captures stop after that moment while trips keep completing.

CREATE TABLE riders (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL,
    city       text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE drivers (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL,
    city       text NOT NULL,
    vehicle    text NOT NULL,
    rating     numeric(3,2) NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE trips (
    id               text PRIMARY KEY,          -- TR-1001 ...
    rider_id         text NOT NULL REFERENCES riders(id),
    driver_id        text NOT NULL REFERENCES drivers(id),
    city             text NOT NULL,
    requested_at     timestamptz NOT NULL,
    completed_at     timestamptz,               -- NULL → not finished
    status           text NOT NULL CHECK (status IN ('completed','in_progress','cancelled')),
    distance_km      numeric(6,2) NOT NULL,
    fare_cents       integer NOT NULL,
    surge_multiplier numeric(3,2) NOT NULL DEFAULT 1.0
);

CREATE TABLE payments (
    id           text PRIMARY KEY,              -- PAY-1001 ...
    trip_id      text NOT NULL REFERENCES trips(id),
    amount_cents integer NOT NULL,
    status       text NOT NULL CHECK (status IN ('captured','pending','failed')),
    provider_ref text,                          -- NULL while pending: the webhook never confirmed
    created_at   timestamptz NOT NULL,
    captured_at  timestamptz
);

-- Completed trips whose money never arrived — the incident's blast radius.
CREATE VIEW v_unpaid_completed_trips AS
SELECT t.id, t.driver_id, t.rider_id, t.completed_at, t.fare_cents, p.status AS payment_status
  FROM trips t
  JOIN payments p ON p.trip_id = t.id
 WHERE t.status = 'completed'
   AND p.status <> 'captured';

-- Per-driver payout gap: what they earned vs what was actually captured.
CREATE VIEW v_driver_payout_gap AS
SELECT d.id AS driver_id,
       d.name,
       count(*) FILTER (WHERE p.status <> 'captured') AS unpaid_trips,
       coalesce(sum(t.fare_cents) FILTER (WHERE p.status <> 'captured'), 0) AS missing_cents
  FROM drivers d
  JOIN trips t    ON t.driver_id = d.id AND t.status = 'completed'
  JOIN payments p ON p.trip_id = t.id
 GROUP BY d.id, d.name
HAVING count(*) FILTER (WHERE p.status <> 'captured') > 0;
