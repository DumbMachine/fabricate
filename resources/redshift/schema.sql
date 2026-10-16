CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE orders (
  position INTEGER PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE payments (
  position INTEGER PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE shipments (
  position INTEGER PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE saas_invoices (
  position INTEGER PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE statements (
  seq INTEGER PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  client_token TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  cluster_identifier TEXT NOT NULL,
  database_name TEXT NOT NULL,
  db_user TEXT NOT NULL,
  secret_arn TEXT NOT NULL DEFAULT '',
  statement_name TEXT NOT NULL DEFAULT '',
  sql_text TEXT NOT NULL,
  parameters_json TEXT NOT NULL,
  result_format TEXT NOT NULL,
  status TEXT NOT NULL,
  has_result_set INTEGER NOT NULL,
  result_rows INTEGER NOT NULL,
  result_size INTEGER NOT NULL,
  records_json TEXT NOT NULL,
  columns_json TEXT NOT NULL,
  redshift_pid INTEGER NOT NULL,
  redshift_query_id INTEGER NOT NULL,
  duration_ns INTEGER NOT NULL
);

CREATE UNIQUE INDEX statements_client_token ON statements(client_token) WHERE client_token <> '';
