CREATE TABLE records (
  collection TEXT NOT NULL,
  id TEXT NOT NULL,
  document TEXT NOT NULL,
  PRIMARY KEY (collection, id)
);
