CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE programs (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  state TEXT NOT NULL,
  type TEXT NOT NULL,
  short_description TEXT NOT NULL
);

CREATE TABLE partners (
  id TEXT PRIMARY KEY,
  campaign_id INTEGER NOT NULL REFERENCES programs(id),
  name TEXT NOT NULL,
  state TEXT NOT NULL,
  currency TEXT NOT NULL,
  website TEXT NOT NULL,
  description TEXT NOT NULL,
  date_created TEXT NOT NULL
);

CREATE TABLE actions (
  id TEXT PRIMARY KEY,
  campaign_id INTEGER NOT NULL REFERENCES programs(id),
  partner_id TEXT NOT NULL REFERENCES partners(id),
  oid TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK (state IN ('PENDING', 'APPROVED', 'REVERSED')),
  payout_paise INTEGER NOT NULL CHECK (payout_paise >= 0),
  amount_paise INTEGER NOT NULL CHECK (amount_paise >= 0),
  currency TEXT NOT NULL CHECK (length(currency) = 3),
  event_date TEXT NOT NULL,
  creation_date TEXT NOT NULL,
  customer_city TEXT NOT NULL,
  customer_region TEXT NOT NULL,
  customer_country TEXT NOT NULL,
  customer_post_code TEXT NOT NULL,
  note TEXT NOT NULL
);

CREATE TABLE action_items (
  action_id TEXT NOT NULL REFERENCES actions(id),
  position INTEGER NOT NULL,
  sku TEXT NOT NULL,
  name TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK (quantity >= 1),
  sale_amount_paise INTEGER NOT NULL CHECK (sale_amount_paise >= 0),
  PRIMARY KEY (action_id, sku)
);

CREATE TABLE notes (
  id TEXT PRIMARY KEY,
  campaign_id INTEGER NOT NULL REFERENCES programs(id),
  partner_id TEXT NOT NULL REFERENCES partners(id),
  creator TEXT NOT NULL,
  content TEXT NOT NULL,
  type TEXT NOT NULL,
  creation_date TEXT NOT NULL,
  modification_date TEXT NOT NULL
);

CREATE INDEX actions_campaign_created ON actions(campaign_id, creation_date DESC, id DESC);
CREATE INDEX notes_campaign_created ON notes(campaign_id, creation_date, id);
