CREATE TABLE organization (
  organization_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  contact_name TEXT NOT NULL,
  email TEXT NOT NULL,
  is_default_org INTEGER NOT NULL,
  language_code TEXT NOT NULL,
  fiscal_year_start_month INTEGER NOT NULL,
  account_created_date TEXT NOT NULL,
  time_zone TEXT NOT NULL,
  is_org_active INTEGER NOT NULL,
  currency_code TEXT NOT NULL,
  currency_symbol TEXT NOT NULL,
  currency_format TEXT NOT NULL,
  price_precision INTEGER NOT NULL
);

CREATE TABLE warehouses (
  warehouse_id TEXT PRIMARY KEY,
  warehouse_name TEXT NOT NULL,
  address TEXT NOT NULL,
  city TEXT NOT NULL,
  state TEXT NOT NULL,
  zip TEXT NOT NULL,
  country TEXT NOT NULL,
  phone TEXT NOT NULL,
  email TEXT NOT NULL,
  is_primary INTEGER NOT NULL,
  status TEXT NOT NULL
);

CREATE TABLE items (
  item_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  sku TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL,
  status TEXT NOT NULL,
  unit TEXT NOT NULL,
  rate INTEGER NOT NULL,
  purchase_rate INTEGER NOT NULL,
  stock_on_hand INTEGER NOT NULL,
  product_type TEXT NOT NULL,
  item_type TEXT NOT NULL,
  warehouse_id TEXT NOT NULL,
  FOREIGN KEY (warehouse_id) REFERENCES warehouses(warehouse_id)
);

CREATE TABLE sales_orders (
  salesorder_id TEXT PRIMARY KEY,
  salesorder_number TEXT NOT NULL UNIQUE,
  reference_number TEXT NOT NULL UNIQUE,
  date TEXT NOT NULL,
  status TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  total INTEGER NOT NULL,
  created_time TEXT NOT NULL,
  warehouse_id TEXT NOT NULL,
  customer_id TEXT NOT NULL,
  customer_name TEXT NOT NULL,
  customer_email TEXT NOT NULL,
  customer_phone TEXT NOT NULL,
  ship_address TEXT NOT NULL,
  ship_city TEXT NOT NULL,
  ship_state TEXT NOT NULL,
  ship_zip TEXT NOT NULL,
  ship_country TEXT NOT NULL,
  FOREIGN KEY (warehouse_id) REFERENCES warehouses(warehouse_id)
);

CREATE TABLE sales_order_lines (
  line_item_id TEXT PRIMARY KEY,
  salesorder_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  name TEXT NOT NULL,
  sku TEXT NOT NULL,
  rate INTEGER NOT NULL,
  quantity INTEGER NOT NULL,
  item_total INTEGER NOT NULL,
  item_order INTEGER NOT NULL,
  FOREIGN KEY (salesorder_id) REFERENCES sales_orders(salesorder_id),
  FOREIGN KEY (item_id) REFERENCES items(item_id)
);

CREATE TABLE purchase_orders (
  purchaseorder_id TEXT PRIMARY KEY,
  purchaseorder_number TEXT NOT NULL UNIQUE,
  date TEXT NOT NULL,
  status TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  total INTEGER NOT NULL,
  created_time TEXT NOT NULL,
  last_modified_time TEXT NOT NULL,
  warehouse_id TEXT NOT NULL,
  vendor_id TEXT NOT NULL,
  vendor_name TEXT NOT NULL,
  approver_name TEXT NOT NULL,
  approver_email TEXT NOT NULL,
  FOREIGN KEY (warehouse_id) REFERENCES warehouses(warehouse_id)
);

CREATE TABLE purchase_order_lines (
  line_item_id TEXT PRIMARY KEY,
  purchaseorder_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  name TEXT NOT NULL,
  sku TEXT NOT NULL,
  purchase_rate INTEGER NOT NULL,
  quantity INTEGER NOT NULL,
  item_total INTEGER NOT NULL,
  item_order INTEGER NOT NULL,
  FOREIGN KEY (purchaseorder_id) REFERENCES purchase_orders(purchaseorder_id),
  FOREIGN KEY (item_id) REFERENCES items(item_id)
);

CREATE TABLE comments (
  comment_id TEXT PRIMARY KEY,
  purchaseorder_id TEXT NOT NULL,
  commented_by TEXT NOT NULL,
  comment_type TEXT NOT NULL,
  operation_type TEXT NOT NULL,
  time TEXT NOT NULL,
  date_description TEXT NOT NULL,
  FOREIGN KEY (purchaseorder_id) REFERENCES purchase_orders(purchaseorder_id)
);

CREATE INDEX sales_order_lines_order ON sales_order_lines(salesorder_id, item_order, line_item_id);
CREATE INDEX purchase_order_lines_order ON purchase_order_lines(purchaseorder_id, item_order, line_item_id);
CREATE INDEX comments_po ON comments(purchaseorder_id, comment_id);
