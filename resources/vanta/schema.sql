CREATE TABLE vendors (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE security_reviews (
  id TEXT PRIMARY KEY,
  vendor_id TEXT NOT NULL REFERENCES vendors(id),
  body TEXT NOT NULL
);

CREATE TABLE tests (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE entities (
  id TEXT PRIMARY KEY,
  test_id TEXT NOT NULL REFERENCES tests(id),
  body TEXT NOT NULL
);

CREATE TABLE documents (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE uploads (
  id TEXT PRIMARY KEY,
  document_id TEXT NOT NULL REFERENCES documents(id),
  body TEXT NOT NULL
);
