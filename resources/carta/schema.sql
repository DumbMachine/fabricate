CREATE TABLE issuer (
  id TEXT PRIMARY KEY,
  legal_name TEXT NOT NULL,
  doing_business_as_name TEXT NOT NULL,
  website TEXT NOT NULL
);

CREATE TABLE stakeholders (
  position INTEGER NOT NULL,
  id TEXT PRIMARY KEY,
  issuer_id TEXT NOT NULL,
  full_name TEXT NOT NULL,
  email TEXT NOT NULL,
  relationship TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  country TEXT NOT NULL
);

CREATE TABLE certificates (
  position INTEGER NOT NULL,
  id TEXT PRIMARY KEY,
  issuer_id TEXT NOT NULL,
  stakeholder_id TEXT NOT NULL,
  share_class_name TEXT NOT NULL,
  security_label TEXT NOT NULL,
  issue_date TEXT NOT NULL,
  quantity TEXT NOT NULL,
  currency TEXT NOT NULL,
  price TEXT NOT NULL
);

CREATE TABLE option_grants (
  position INTEGER NOT NULL,
  id TEXT PRIMARY KEY,
  issuer_id TEXT NOT NULL,
  stakeholder_id TEXT NOT NULL,
  plan_name TEXT NOT NULL,
  security_label TEXT NOT NULL,
  stock_option_type TEXT NOT NULL,
  issue_date TEXT NOT NULL,
  quantity TEXT NOT NULL,
  outstanding_quantity TEXT NOT NULL,
  currency TEXT NOT NULL,
  exercise_price TEXT NOT NULL
);

CREATE TABLE fair_market_values (
  position INTEGER NOT NULL,
  id TEXT PRIMARY KEY,
  effective_date TEXT NOT NULL,
  expiration_date TEXT NOT NULL,
  valuations TEXT NOT NULL
);

CREATE TABLE draft_sets (
  id TEXT PRIMARY KEY,
  issuer_id TEXT NOT NULL,
  created_seq INTEGER NOT NULL
);

CREATE TABLE draft_option_grants (
  position INTEGER NOT NULL,
  id TEXT PRIMARY KEY,
  issuer_id TEXT NOT NULL,
  set_id TEXT NOT NULL,
  body TEXT NOT NULL
);
