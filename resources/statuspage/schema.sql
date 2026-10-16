CREATE TABLE pages (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  subdomain TEXT NOT NULL,
  domain TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  page_description TEXT NOT NULL DEFAULT '',
  time_zone TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE components (
  id TEXT PRIMARY KEY,
  page_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  position INTEGER NOT NULL,
  showcase INTEGER NOT NULL DEFAULT 0,
  is_group INTEGER NOT NULL DEFAULT 0,
  only_show_if_degraded INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (page_id) REFERENCES pages(id)
);

CREATE TABLE incidents (
  id TEXT PRIMARY KEY,
  page_id TEXT NOT NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  impact TEXT NOT NULL,
  impact_override TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT '',
  monitoring_at TEXT NOT NULL DEFAULT '',
  postmortem_body TEXT NOT NULL DEFAULT '',
  FOREIGN KEY (page_id) REFERENCES pages(id)
);

CREATE TABLE incident_components (
  incident_id TEXT NOT NULL,
  component_id TEXT NOT NULL,
  PRIMARY KEY (incident_id, component_id),
  FOREIGN KEY (incident_id) REFERENCES incidents(id),
  FOREIGN KEY (component_id) REFERENCES components(id)
);

CREATE TABLE incident_updates (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL,
  status TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  display_at TEXT NOT NULL,
  deliver_notifications INTEGER NOT NULL DEFAULT 1,
  wants_twitter_update INTEGER NOT NULL DEFAULT 0,
  affected_json TEXT NOT NULL,
  FOREIGN KEY (incident_id) REFERENCES incidents(id)
);

CREATE INDEX components_page ON components(page_id, position, id);
CREATE INDEX incidents_page ON incidents(page_id, created_at, id);
CREATE INDEX incident_updates_incident ON incident_updates(incident_id, created_at, id);
