CREATE TABLE companies (
  gsid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  industry TEXT NOT NULL DEFAULT '',
  arr REAL,
  employees INTEGER,
  lifecycle_in_weeks INTEGER,
  original_contract_date TEXT NOT NULL DEFAULT '',
  renewal_date TEXT NOT NULL DEFAULT '',
  stage TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  health TEXT NOT NULL DEFAULT '' CHECK (health IN ('', 'Red', 'Yellow', 'Green')),
  csm_first_name TEXT NOT NULL DEFAULT '',
  csm_last_name TEXT NOT NULL DEFAULT '',
  csm_email TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL DEFAULT '',
  created_date INTEGER NOT NULL,
  modified_date INTEGER NOT NULL
);

CREATE TABLE success_plans (
  gsid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  company_id TEXT NOT NULL,
  owner_email TEXT NOT NULL DEFAULT '',
  owner_name TEXT NOT NULL DEFAULT '',
  due_date TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  action_plan TEXT NOT NULL DEFAULT '',
  entity_type TEXT NOT NULL DEFAULT 'COMPANY'
);

CREATE TABLE ctas (
  gsid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  company_id TEXT NOT NULL,
  success_plan_id TEXT NOT NULL DEFAULT '',
  owner_email TEXT NOT NULL DEFAULT '',
  owner_name TEXT NOT NULL DEFAULT '',
  due_date TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  priority TEXT NOT NULL DEFAULT '',
  comments TEXT NOT NULL DEFAULT '',
  entity_type TEXT NOT NULL DEFAULT 'COMPANY',
  is_closed INTEGER NOT NULL DEFAULT 0 CHECK (is_closed IN (0, 1)),
  is_important INTEGER NOT NULL DEFAULT 0 CHECK (is_important IN (0, 1))
);

CREATE TABLE activities (
  gsid TEXT PRIMARY KEY,
  external_id TEXT NOT NULL UNIQUE,
  context_name TEXT NOT NULL,
  context_id TEXT NOT NULL DEFAULT '',
  company_id TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  author_id TEXT NOT NULL DEFAULT '',
  type_name TEXT NOT NULL,
  subject TEXT NOT NULL,
  notes TEXT NOT NULL,
  activity_date TEXT NOT NULL
);

CREATE INDEX ctas_company ON ctas(company_id, gsid);
CREATE INDEX success_plans_company ON success_plans(company_id, gsid);
CREATE INDEX activities_external ON activities(external_id);
