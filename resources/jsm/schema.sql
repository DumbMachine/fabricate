CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE service_desks (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  project_key TEXT NOT NULL UNIQUE,
  project_name TEXT NOT NULL
);

CREATE TABLE request_types (
  id TEXT PRIMARY KEY,
  service_desk_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  help_text TEXT NOT NULL,
  issue_type_id TEXT NOT NULL,
  FOREIGN KEY (service_desk_id) REFERENCES service_desks(id)
);

CREATE TABLE users (
  account_id TEXT PRIMARY KEY,
  email_address TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  active INTEGER NOT NULL CHECK (active IN (0, 1)),
  time_zone TEXT NOT NULL
);

CREATE TABLE requests (
  issue_id TEXT PRIMARY KEY,
  issue_key TEXT NOT NULL UNIQUE,
  service_desk_id TEXT NOT NULL,
  request_type_id TEXT NOT NULL,
  summary TEXT NOT NULL,
  description TEXT NOT NULL,
  reporter_account_id TEXT NOT NULL,
  status TEXT NOT NULL,
  status_category TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  participant_account_ids TEXT NOT NULL,
  FOREIGN KEY (service_desk_id) REFERENCES service_desks(id),
  FOREIGN KEY (request_type_id) REFERENCES request_types(id),
  FOREIGN KEY (reporter_account_id) REFERENCES users(account_id)
);

CREATE TABLE comments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  body TEXT NOT NULL,
  is_public INTEGER NOT NULL CHECK (is_public IN (0, 1)),
  author_account_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (issue_id) REFERENCES requests(issue_id),
  FOREIGN KEY (author_account_id) REFERENCES users(account_id)
);

CREATE INDEX requests_desk ON requests(service_desk_id, issue_key);
CREATE INDEX comments_request ON comments(issue_id, created_at, id);
