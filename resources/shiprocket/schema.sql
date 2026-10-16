CREATE TABLE account (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  company_id INTEGER NOT NULL,
  user_id INTEGER NOT NULL,
  first_name TEXT NOT NULL,
  last_name TEXT NOT NULL,
  email TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE couriers (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE pickups (
  id TEXT PRIMARY KEY,
  location TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  email TEXT NOT NULL,
  phone TEXT NOT NULL,
  address TEXT NOT NULL,
  address_2 TEXT NOT NULL,
  city TEXT NOT NULL,
  state TEXT NOT NULL,
  country TEXT NOT NULL,
  pincode TEXT NOT NULL
);

CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  channel_order_id TEXT NOT NULL UNIQUE,
  channel_name TEXT NOT NULL,
  status TEXT NOT NULL,
  awb TEXT NOT NULL,
  courier_id INTEGER NOT NULL,
  courier_name TEXT NOT NULL,
  shipment_id TEXT NOT NULL UNIQUE,
  customer_name TEXT NOT NULL,
  customer_email TEXT NOT NULL,
  customer_phone TEXT NOT NULL,
  address TEXT NOT NULL,
  address_2 TEXT NOT NULL,
  city TEXT NOT NULL,
  state TEXT NOT NULL,
  pincode TEXT NOT NULL,
  country TEXT NOT NULL,
  pickup_location TEXT NOT NULL,
  payment_method TEXT NOT NULL,
  payment_status TEXT NOT NULL,
  sub_total INTEGER NOT NULL,
  weight_grams INTEGER NOT NULL,
  length_cm INTEGER NOT NULL,
  breadth_cm INTEGER NOT NULL,
  height_cm INTEGER NOT NULL,
  order_date TEXT NOT NULL,
  ndr_reason TEXT NOT NULL,
  ndr_attempts INTEGER NOT NULL,
  ndr_action TEXT NOT NULL,
  ndr_comments TEXT NOT NULL,
  assigned_at TEXT NOT NULL,
  items_json TEXT NOT NULL
);

CREATE TABLE returns (
  id TEXT PRIMARY KEY,
  order_id TEXT NOT NULL UNIQUE,
  shipment_id TEXT NOT NULL UNIQUE,
  channel_name TEXT NOT NULL,
  status TEXT NOT NULL,
  forward_shipment_status TEXT NOT NULL,
  customer_name TEXT NOT NULL,
  customer_email TEXT NOT NULL,
  customer_phone TEXT NOT NULL,
  address TEXT NOT NULL,
  address_2 TEXT NOT NULL,
  city TEXT NOT NULL,
  state TEXT NOT NULL,
  pincode TEXT NOT NULL,
  country TEXT NOT NULL,
  pickup_location TEXT NOT NULL,
  sub_total INTEGER NOT NULL,
  order_date TEXT NOT NULL,
  awb TEXT NOT NULL,
  courier_name TEXT NOT NULL,
  items_json TEXT NOT NULL
);
