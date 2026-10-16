CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE files (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  description TEXT NOT NULL,
  body TEXT NOT NULL,
  parents TEXT NOT NULL,
  starred INTEGER NOT NULL CHECK (starred IN (0, 1)),
  trashed INTEGER NOT NULL CHECK (trashed IN (0, 1)),
  explicitly_trashed INTEGER NOT NULL CHECK (explicitly_trashed IN (0, 1)),
  created_time TEXT NOT NULL,
  modified_time TEXT NOT NULL,
  folder_color_rgb TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL CHECK (version >= 1)
);

CREATE TABLE permissions (
  file_id TEXT NOT NULL,
  id TEXT NOT NULL,
  type TEXT NOT NULL,
  role TEXT NOT NULL,
  email_address TEXT NOT NULL,
  display_name TEXT NOT NULL,
  allow_file_discovery INTEGER NOT NULL CHECK (allow_file_discovery IN (0, 1)),
  PRIMARY KEY (file_id, id)
);

CREATE INDEX permissions_file ON permissions(file_id, id);
CREATE INDEX files_name ON files(name, id);
