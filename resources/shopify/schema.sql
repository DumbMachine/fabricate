CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE sequences (
  name TEXT PRIMARY KEY,
  value INTEGER NOT NULL
);

CREATE TABLE shops (
  id INTEGER PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  body TEXT NOT NULL
);

CREATE TABLE products (
  id INTEGER PRIMARY KEY,
  shop TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE customers (
  id INTEGER PRIMARY KEY,
  shop TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  shop TEXT NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE fulfillments (
  id INTEGER PRIMARY KEY,
  shop TEXT NOT NULL,
  order_id INTEGER NOT NULL,
  body TEXT NOT NULL
);

CREATE TABLE refunds (
  id INTEGER PRIMARY KEY,
  shop TEXT NOT NULL,
  order_id INTEGER NOT NULL,
  body TEXT NOT NULL
);

CREATE INDEX products_shop ON products(shop, id);
CREATE INDEX customers_shop ON customers(shop, id);
CREATE INDEX orders_shop ON orders(shop, id);
CREATE INDEX fulfillments_order ON fulfillments(shop, order_id, id);
CREATE INDEX refunds_order ON refunds(shop, order_id, id);
