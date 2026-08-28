CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE workspaces (
  gid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  is_organization INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE users (
  gid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL
);

CREATE TABLE projects (
  gid TEXT PRIMARY KEY,
  workspace_gid TEXT NOT NULL,
  name TEXT NOT NULL,
  archived INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY (workspace_gid) REFERENCES workspaces(gid)
);

CREATE TABLE sections (
  gid TEXT PRIMARY KEY,
  project_gid TEXT NOT NULL,
  name TEXT NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY (project_gid) REFERENCES projects(gid)
);

CREATE TABLE tasks (
  gid TEXT PRIMARY KEY,
  workspace_gid TEXT NOT NULL,
  name TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  completed INTEGER NOT NULL DEFAULT 0,
  assignee_gid TEXT,
  due_on TEXT,
  created_at TEXT NOT NULL,
  modified_at TEXT NOT NULL,
  FOREIGN KEY (workspace_gid) REFERENCES workspaces(gid),
  FOREIGN KEY (assignee_gid) REFERENCES users(gid)
);

CREATE TABLE task_projects (
  task_gid TEXT NOT NULL,
  project_gid TEXT NOT NULL,
  section_gid TEXT,
  PRIMARY KEY (task_gid, project_gid),
  FOREIGN KEY (task_gid) REFERENCES tasks(gid),
  FOREIGN KEY (project_gid) REFERENCES projects(gid),
  FOREIGN KEY (section_gid) REFERENCES sections(gid)
);

CREATE TABLE stories (
  gid TEXT PRIMARY KEY,
  task_gid TEXT NOT NULL,
  text TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  resource_subtype TEXT NOT NULL DEFAULT 'comment_added',
  FOREIGN KEY (task_gid) REFERENCES tasks(gid),
  FOREIGN KEY (created_by) REFERENCES users(gid)
);

CREATE INDEX tasks_workspace ON tasks(workspace_gid, gid);
CREATE INDEX task_projects_project ON task_projects(project_gid, task_gid);
CREATE INDEX stories_task ON stories(task_gid, created_at, gid);
