CREATE TABLE properties (
  property_id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  time_zone TEXT NOT NULL
);

CREATE TABLE events (
  property_id TEXT NOT NULL REFERENCES properties(property_id),
  event_date TEXT NOT NULL,
  event_name TEXT NOT NULL,
  transaction_id TEXT NOT NULL,
  session_source TEXT NOT NULL,
  session_medium TEXT NOT NULL,
  session_campaign_name TEXT NOT NULL,
  event_count INTEGER NOT NULL CHECK (event_count >= 0),
  transactions INTEGER NOT NULL CHECK (transactions >= 0),
  purchase_revenue INTEGER NOT NULL CHECK (purchase_revenue >= 0),
  item_refund_amount INTEGER NOT NULL CHECK (item_refund_amount >= 0),
  PRIMARY KEY (property_id, transaction_id, event_name)
);

CREATE TABLE audience_exports (
  name TEXT PRIMARY KEY,
  property_id TEXT NOT NULL REFERENCES properties(property_id),
  audience TEXT NOT NULL,
  audience_display_name TEXT NOT NULL,
  dimensions TEXT NOT NULL,
  state TEXT NOT NULL,
  row_count INTEGER NOT NULL CHECK (row_count >= 0),
  percentage_completed REAL NOT NULL,
  begin_creating_time TEXT NOT NULL,
  creation_quota_tokens_charged INTEGER NOT NULL,
  error_message TEXT NOT NULL
);

CREATE INDEX events_property_date ON events(property_id, event_date, transaction_id, event_name);
CREATE INDEX audience_exports_property_name ON audience_exports(property_id, name);
