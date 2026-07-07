-- A deliberately under-indexed schema. order_items only has a PK
-- on id; the FK to orders.id is unindexed, so every join from
-- orders → order_items is a full scan. That's the bug the agent
-- should find via EXPLAIN.
CREATE TABLE users (
  id         BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email      VARCHAR(255) NOT NULL UNIQUE,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
  id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id     BIGINT NOT NULL,
  status      VARCHAR(32) NOT NULL,
  total_cents BIGINT NOT NULL,
  placed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_orders_user_id (user_id)
);

CREATE TABLE order_items (
  id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  order_id    BIGINT NOT NULL,
  sku         VARCHAR(32) NOT NULL,
  qty         INT NOT NULL,
  price_cents BIGINT NOT NULL
  -- NOTE: no index on order_id. This is the bug.
);
