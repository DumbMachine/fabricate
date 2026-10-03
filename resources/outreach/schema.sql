CREATE TABLE sequences (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  sequence_type TEXT NOT NULL,
  share_type TEXT NOT NULL,
  tags TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  position INTEGER NOT NULL
);

CREATE TABLE prospects (
  id TEXT PRIMARY KEY,
  first_name TEXT NOT NULL,
  last_name TEXT NOT NULL,
  name TEXT NOT NULL,
  emails TEXT NOT NULL,
  title TEXT NOT NULL,
  company TEXT NOT NULL,
  mobile_phones TEXT NOT NULL,
  tags TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  position INTEGER NOT NULL
);

CREATE TABLE sequence_states (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  prospect_id TEXT NOT NULL,
  sequence_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  state_changed_at TEXT NOT NULL,
  position INTEGER NOT NULL
);

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  note TEXT NOT NULL,
  completed INTEGER NOT NULL,
  state TEXT NOT NULL,
  task_type TEXT NOT NULL,
  due_at TEXT NOT NULL,
  completed_at TEXT NOT NULL,
  state_changed_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  prospect_id TEXT NOT NULL,
  sequence_id TEXT NOT NULL,
  position INTEGER NOT NULL
);

CREATE INDEX sequence_states_pair ON sequence_states(sequence_id, prospect_id);
CREATE INDEX tasks_sequence ON tasks(sequence_id, position);
CREATE INDEX tasks_prospect ON tasks(prospect_id, position);
