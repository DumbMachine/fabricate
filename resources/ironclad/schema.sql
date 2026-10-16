CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE workflows (
  id TEXT PRIMARY KEY,
  ironclad_id TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  template TEXT NOT NULL,
  step TEXT NOT NULL,
  status TEXT NOT NULL,
  is_cancelled INTEGER NOT NULL,
  is_complete INTEGER NOT NULL,
  counterparty_name TEXT NOT NULL,
  counterparty_email TEXT NOT NULL DEFAULT '',
  envelope_id TEXT NOT NULL DEFAULT '',
  filename TEXT NOT NULL DEFAULT '',
  record_ids TEXT NOT NULL,
  role_id TEXT NOT NULL,
  role_display_name TEXT NOT NULL,
  assignee_id TEXT NOT NULL,
  created TEXT NOT NULL,
  last_updated TEXT NOT NULL,
  creator_id TEXT NOT NULL
);

CREATE TABLE records (
  id TEXT PRIMARY KEY,
  ironclad_id TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  last_updated TEXT NOT NULL,
  counterparty_name TEXT NOT NULL,
  envelope_id TEXT NOT NULL DEFAULT '',
  filename TEXT NOT NULL DEFAULT '',
  workflow_id TEXT NOT NULL,
  contract_status TEXT NOT NULL,
  enhanced_status TEXT NOT NULL,
  FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

CREATE TABLE comments (
  id TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL,
  message TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  author_id TEXT NOT NULL,
  replied_to TEXT NOT NULL DEFAULT '',
  FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

CREATE TABLE approval_groups (
  workflow_id TEXT NOT NULL,
  role TEXT NOT NULL,
  display_name TEXT NOT NULL,
  reviewer_type TEXT NOT NULL,
  reviewer_status TEXT NOT NULL,
  status TEXT NOT NULL,
  sort_order INTEGER NOT NULL,
  PRIMARY KEY (workflow_id, role),
  FOREIGN KEY (workflow_id) REFERENCES workflows(id)
);

CREATE INDEX records_workflow ON records(workflow_id, id);
CREATE INDEX comments_workflow ON comments(workflow_id, timestamp, id);
CREATE INDEX approval_groups_workflow ON approval_groups(workflow_id, sort_order, role);
