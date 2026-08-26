CREATE TABLE objects (
  object_type TEXT NOT NULL,
  id TEXT NOT NULL,
  properties TEXT NOT NULL,
  archived INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (object_type, id)
);

CREATE INDEX objects_type ON objects(object_type, id);
