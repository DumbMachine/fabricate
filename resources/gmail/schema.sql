CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE labels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('system', 'user'))
);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  from_addr TEXT NOT NULL,
  to_addr TEXT NOT NULL,
  subject TEXT NOT NULL,
  body TEXT NOT NULL,
  internal_date INTEGER NOT NULL CHECK (internal_date >= 0),
  label_ids TEXT NOT NULL
);

CREATE INDEX messages_thread_date ON messages(thread_id, internal_date, id);
CREATE INDEX messages_date ON messages(internal_date DESC, id);
