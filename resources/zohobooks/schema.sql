CREATE TABLE organizations (
  organization_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  contact_name TEXT NOT NULL,
  email TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  currency_symbol TEXT NOT NULL,
  time_zone TEXT NOT NULL,
  is_org_active INTEGER NOT NULL
);

CREATE TABLE contacts (
  contact_id TEXT PRIMARY KEY,
  contact_name TEXT NOT NULL,
  company_name TEXT NOT NULL,
  contact_type TEXT NOT NULL,
  customer_sub_type TEXT NOT NULL,
  email TEXT NOT NULL,
  phone TEXT NOT NULL,
  first_name TEXT NOT NULL,
  last_name TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  notes TEXT NOT NULL,
  status TEXT NOT NULL,
  address TEXT NOT NULL,
  city TEXT NOT NULL,
  state TEXT NOT NULL,
  zip TEXT NOT NULL,
  country TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE invoices (
  invoice_id TEXT PRIMARY KEY,
  invoice_number TEXT NOT NULL UNIQUE,
  customer_id TEXT NOT NULL REFERENCES contacts(contact_id),
  status TEXT NOT NULL,
  date TEXT NOT NULL,
  due_date TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  reference_number TEXT NOT NULL,
  notes TEXT NOT NULL,
  total REAL NOT NULL,
  balance REAL NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE invoice_lines (
  invoice_id TEXT NOT NULL REFERENCES invoices(invoice_id),
  position INTEGER NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  rate REAL NOT NULL,
  quantity REAL NOT NULL,
  PRIMARY KEY (invoice_id, position)
);

CREATE TABLE credit_notes (
  creditnote_id TEXT PRIMARY KEY,
  creditnote_number TEXT NOT NULL UNIQUE,
  customer_id TEXT NOT NULL REFERENCES contacts(contact_id),
  invoice_id TEXT NOT NULL REFERENCES invoices(invoice_id),
  status TEXT NOT NULL,
  date TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  reference_number TEXT NOT NULL,
  notes TEXT NOT NULL,
  total REAL NOT NULL,
  balance REAL NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE customer_payments (
  payment_id TEXT PRIMARY KEY,
  customer_id TEXT NOT NULL REFERENCES contacts(contact_id),
  payment_mode TEXT NOT NULL,
  amount REAL NOT NULL,
  date TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  reference_number TEXT NOT NULL,
  description TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE payment_invoices (
  payment_id TEXT NOT NULL REFERENCES customer_payments(payment_id),
  invoice_id TEXT NOT NULL REFERENCES invoices(invoice_id),
  amount_applied REAL NOT NULL,
  PRIMARY KEY (payment_id, invoice_id)
);
