CREATE TABLE app (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  phone TEXT NOT NULL,
  about TEXT NOT NULL,
  business_json TEXT NOT NULL,
  profile_json TEXT NOT NULL
);

CREATE TABLE templates (
  id TEXT PRIMARY KEY,
  element_name TEXT NOT NULL,
  category TEXT NOT NULL,
  status TEXT NOT NULL,
  language_code TEXT NOT NULL,
  template_type TEXT NOT NULL,
  data TEXT NOT NULL,
  position INTEGER NOT NULL
);

CREATE TABLE messages (
  message_id TEXT PRIMARY KEY,
  direction TEXT NOT NULL,
  source TEXT NOT NULL,
  destination TEXT NOT NULL,
  type TEXT NOT NULL,
  text TEXT NOT NULL,
  status TEXT NOT NULL,
  sender_name TEXT NOT NULL,
  context_gs_id TEXT NOT NULL,
  read INTEGER NOT NULL CHECK (read IN (0, 1)),
  timestamp INTEGER NOT NULL CHECK (timestamp >= 0),
  position INTEGER NOT NULL
);

CREATE INDEX messages_timestamp ON messages(timestamp, message_id);
CREATE INDEX templates_position ON templates(position, id);
