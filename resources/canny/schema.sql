CREATE TABLE boards (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created TEXT NOT NULL,
  is_private INTEGER NOT NULL CHECK (is_private IN (0, 1)),
  private_comments INTEGER NOT NULL CHECK (private_comments IN (0, 1))
);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL,
  is_admin INTEGER NOT NULL CHECK (is_admin IN (0, 1)),
  created TEXT NOT NULL,
  user_id TEXT NOT NULL
);

CREATE TABLE posts (
  id TEXT PRIMARY KEY,
  board_id TEXT NOT NULL REFERENCES boards(id),
  author_id TEXT NOT NULL REFERENCES users(id),
  by_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  details TEXT NOT NULL,
  status TEXT NOT NULL,
  created TEXT NOT NULL,
  status_changed_at TEXT NOT NULL
);

CREATE TABLE votes (
  id TEXT PRIMARY KEY,
  post_id TEXT NOT NULL REFERENCES posts(id),
  voter_id TEXT NOT NULL REFERENCES users(id),
  by_id TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL,
  vote_priority TEXT NOT NULL,
  UNIQUE (post_id, voter_id)
);
