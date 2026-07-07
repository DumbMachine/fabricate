-- Synthetic seed: 500 users, ~5k orders, ~25k line items.
-- Generated procedurally so this file stays under a few KB.

DELIMITER //
CREATE PROCEDURE seed_data()
BEGIN
  DECLARE i INT DEFAULT 1;
  DECLARE j INT DEFAULT 0;
  DECLARE order_count INT DEFAULT 0;

  WHILE i <= 500 DO
    INSERT INTO users (email) VALUES (CONCAT('user', i, '@example.com'));
    SET i = i + 1;
  END WHILE;

  SET i = 1;
  WHILE i <= 5000 DO
    INSERT INTO orders (user_id, status, total_cents)
      VALUES (1 + FLOOR(RAND() * 500),
              ELT(1 + FLOOR(RAND() * 3), 'pending', 'paid', 'shipped'),
              FLOOR(1000 + RAND() * 50000));
    SET i = i + 1;
  END WHILE;

  SET order_count = (SELECT COUNT(*) FROM orders);
  SET i = 1;
  WHILE i <= 25000 DO
    INSERT INTO order_items (order_id, sku, qty, price_cents)
      VALUES (1 + FLOOR(RAND() * order_count),
              CONCAT('SKU-', LPAD(FLOOR(RAND() * 200), 4, '0')),
              1 + FLOOR(RAND() * 5),
              FLOOR(500 + RAND() * 10000));
    SET i = i + 1;
  END WHILE;
END //
DELIMITER ;

CALL seed_data();
DROP PROCEDURE seed_data;

-- Pre-warm a slow-query baseline: long_query_time set low + log_output to TABLE.
SET GLOBAL slow_query_log = 'ON';
SET GLOBAL long_query_time = 0.05;
SET GLOBAL log_output = 'TABLE';
