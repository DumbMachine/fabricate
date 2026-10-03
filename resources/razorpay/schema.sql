CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE payments (
  id TEXT PRIMARY KEY,
  order_id TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE refunds (
  id TEXT PRIMARY KEY,
  payment_id TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE payment_links (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);
