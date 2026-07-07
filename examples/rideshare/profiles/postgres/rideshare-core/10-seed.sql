-- Deterministic seed: no random(), so every create is identical.
-- Timeline anchor: the payments-webhook broke 2026-07-03 14:05 UTC.

-- 20 riders, 12 drivers.
INSERT INTO riders (id, name, email, city, created_at)
SELECT format('rid-%s', lpad(gs::text, 2, '0')),
       (ARRAY['Ada Lin','Bo Marsh','Cleo Ruiz','Dev Patel','Efe Osei','Fay Wong','Gus Beck','Hana Kato',
              'Ivo Metz','Jia Chen','Kofi Adjei','Lea Voss','Mo Farah','Nia Bell','Omar Diaz','Pia Novak',
              'Quinn Roy','Rui Costa','Sara Meyer','Dana Whitfield'])[gs],
       format('rider%s@example.net', gs),
       'san-francisco',
       timestamptz '2026-05-01 09:00Z' + (gs || ' hours')::interval
  FROM generate_series(1, 20) gs;

INSERT INTO drivers (id, name, email, city, vehicle, rating, created_at)
SELECT format('drv-%s', lpad(gs::text, 2, '0')),
       (ARRAY['Marta Silva','Yusuf Demir','Elena Petrova','Jack Doyle','Priya Nair','Tomas Berg',
              'Aiko Mori','Sam Okafor','Luca Ricci','Wera Nowak','Chen Wei','Rosa Marin'])[gs],
       format('driver%s@example.net', gs),
       'san-francisco',
       (ARRAY['Toyota Prius','Honda Civic','Tesla Model 3','Hyundai Ioniq','Kia Niro','VW ID.4'])[1 + gs % 6],
       4.5 + (gs % 5) * 0.1,
       timestamptz '2026-04-15 08:00Z' + (gs || ' hours')::interval
  FROM generate_series(1, 12) gs;

-- TR-1001..TR-1040: the healthy era (Jun 25 → Jul 3 morning).
-- Completed trips, payments captured minutes after completion.
INSERT INTO trips (id, rider_id, driver_id, city, requested_at, completed_at, status, distance_km, fare_cents, surge_multiplier)
SELECT format('TR-%s', 1000 + gs),
       format('rid-%s', lpad((1 + gs % 20)::text, 2, '0')),
       format('drv-%s', lpad((1 + gs % 12)::text, 2, '0')),
       'san-francisco',
       timestamptz '2026-06-25 06:30Z' + (gs * 5 || ' hours')::interval,
       timestamptz '2026-06-25 06:30Z' + (gs * 5 || ' hours')::interval + ((14 + gs % 20) || ' minutes')::interval,
       'completed',
       round((2.0 + (gs % 9) * 1.3)::numeric, 2),
       700 + (gs % 9) * 260 + (gs % 4) * 90,
       CASE WHEN gs % 7 = 0 THEN 1.4 ELSE 1.0 END
  FROM generate_series(1, 40) gs;

INSERT INTO payments (id, trip_id, amount_cents, status, provider_ref, created_at, captured_at)
SELECT format('PAY-%s', 1000 + gs),
       format('TR-%s', 1000 + gs),
       t.fare_cents,
       'captured',
       format('ch_wheelio_%s', 9200 + gs),
       t.completed_at,
       t.completed_at + interval '3 minutes'
  FROM generate_series(1, 40) gs
  JOIN trips t ON t.id = format('TR-%s', 1000 + gs);

-- TR-1041..TR-1054: the incident era (after Jul 3 14:05 UTC).
-- Trips complete normally; captures never happen — the webhook that
-- confirms them is down. 14 rows of blast radius.
INSERT INTO trips (id, rider_id, driver_id, city, requested_at, completed_at, status, distance_km, fare_cents, surge_multiplier)
SELECT format('TR-%s', 1040 + gs),
       format('rid-%s', lpad((1 + (gs * 3) % 20)::text, 2, '0')),
       format('drv-%s', lpad((1 + (gs * 5) % 12)::text, 2, '0')),
       'san-francisco',
       timestamptz '2026-07-03 14:20Z' + (gs * 6 || ' hours')::interval,
       timestamptz '2026-07-03 14:20Z' + (gs * 6 || ' hours')::interval + ((11 + gs % 17) || ' minutes')::interval,
       'completed',
       round((2.5 + (gs % 7) * 1.6)::numeric, 2),
       800 + (gs % 7) * 310 + (gs % 3) * 120,
       CASE WHEN gs % 5 = 0 THEN 1.8 ELSE 1.0 END
  FROM generate_series(1, 14) gs;

INSERT INTO payments (id, trip_id, amount_cents, status, provider_ref, created_at, captured_at)
SELECT format('PAY-%s', 1040 + gs),
       format('TR-%s', 1040 + gs),
       t.fare_cents,
       'pending',
       NULL,
       t.completed_at,
       NULL
  FROM generate_series(1, 14) gs
  JOIN trips t ON t.id = format('TR-%s', 1040 + gs);

-- TR-1055..TR-1058: live right now — two in progress, two cancelled.
INSERT INTO trips (id, rider_id, driver_id, city, requested_at, completed_at, status, distance_km, fare_cents, surge_multiplier) VALUES
  ('TR-1055', 'rid-03', 'drv-02', 'san-francisco', timestamptz '2026-07-07 16:41Z', NULL, 'in_progress', 4.10, 1240, 1.0),
  ('TR-1056', 'rid-11', 'drv-07', 'san-francisco', timestamptz '2026-07-07 16:52Z', NULL, 'in_progress', 7.90, 2210, 1.8),
  ('TR-1057', 'rid-08', 'drv-04', 'san-francisco', timestamptz '2026-07-07 15:03Z', NULL, 'cancelled',   0.00,    0, 1.0),
  ('TR-1058', 'rid-16', 'drv-09', 'san-francisco', timestamptz '2026-07-06 22:17Z', NULL, 'cancelled',   0.00,    0, 1.0);
