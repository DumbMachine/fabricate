CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE team (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  domain TEXT NOT NULL,
  url TEXT NOT NULL
);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  real_name TEXT NOT NULL,
  email TEXT NOT NULL UNIQUE,
  phone TEXT NOT NULL,
  title TEXT NOT NULL,
  is_owner INTEGER NOT NULL CHECK (is_owner IN (0, 1)),
  is_admin INTEGER NOT NULL CHECK (is_admin IN (0, 1)),
  is_bot INTEGER NOT NULL CHECK (is_bot IN (0, 1)),
  deleted INTEGER NOT NULL CHECK (deleted IN (0, 1))
);

CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  creator TEXT NOT NULL,
  is_private INTEGER NOT NULL CHECK (is_private IN (0, 1)),
  is_archived INTEGER NOT NULL CHECK (is_archived IN (0, 1)),
  topic TEXT NOT NULL,
  purpose TEXT NOT NULL,
  created INTEGER NOT NULL,
  FOREIGN KEY (creator) REFERENCES users(id)
);

CREATE TABLE channel_members (
  channel_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  PRIMARY KEY (channel_id, user_id),
  FOREIGN KEY (channel_id) REFERENCES channels(id),
  FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE messages (
  channel_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  text TEXT NOT NULL,
  ts TEXT NOT NULL,
  thread_ts TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (channel_id, ts),
  FOREIGN KEY (channel_id) REFERENCES channels(id),
  FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX messages_channel_ts ON messages(channel_id, ts);
CREATE INDEX messages_thread ON messages(channel_id, thread_ts);
