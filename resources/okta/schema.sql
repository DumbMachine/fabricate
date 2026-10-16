CREATE TABLE users (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  created TEXT NOT NULL,
  activated TEXT,
  status_changed TEXT,
  last_login TEXT,
  last_updated TEXT NOT NULL,
  first_name TEXT NOT NULL DEFAULT '',
  last_name TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL,
  login TEXT NOT NULL,
  primary_phone TEXT,
  title TEXT,
  department TEXT,
  position INTEGER NOT NULL
);

CREATE TABLE groups (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  created TEXT NOT NULL,
  last_updated TEXT NOT NULL,
  last_membership_updated TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  position INTEGER NOT NULL
);

CREATE TABLE group_users (
  group_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  PRIMARY KEY (group_id, user_id),
  FOREIGN KEY (group_id) REFERENCES groups(id),
  FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE apps (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  label TEXT NOT NULL,
  status TEXT NOT NULL,
  sign_on_mode TEXT NOT NULL,
  created TEXT NOT NULL,
  last_updated TEXT NOT NULL,
  notes_admin TEXT,
  sso_acs_url TEXT,
  position INTEGER NOT NULL
);

CREATE TABLE app_users (
  app_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  scope TEXT NOT NULL,
  status TEXT NOT NULL,
  position INTEGER NOT NULL,
  PRIMARY KEY (app_id, user_id),
  FOREIGN KEY (app_id) REFERENCES apps(id),
  FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX users_position ON users(position, id);
CREATE INDEX groups_position ON groups(position, id);
CREATE INDEX apps_position ON apps(position, id);
CREATE INDEX group_users_position ON group_users(group_id, position, user_id);
CREATE INDEX app_users_position ON app_users(app_id, position, user_id);
