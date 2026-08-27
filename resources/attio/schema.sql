CREATE TABLE objects (
  api_slug TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE records (
  object_slug TEXT NOT NULL,
  record_id TEXT NOT NULL,
  body TEXT NOT NULL,
  PRIMARY KEY (object_slug, record_id)
);

CREATE TABLE workspace_members (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);
