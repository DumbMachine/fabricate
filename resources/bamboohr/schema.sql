CREATE TABLE metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE company (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  domain TEXT NOT NULL
);

CREATE TABLE departments (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE employees (
  id TEXT PRIMARY KEY,
  first_name TEXT NOT NULL,
  last_name TEXT NOT NULL,
  preferred_name TEXT NOT NULL,
  work_email TEXT NOT NULL,
  mobile_phone TEXT NOT NULL,
  job_title_name TEXT NOT NULL,
  department_id TEXT,
  status TEXT NOT NULL CHECK (status IN ('Active', 'Inactive')),
  employment_status TEXT NOT NULL,
  hire_date TEXT,
  location TEXT NOT NULL,
  FOREIGN KEY (department_id) REFERENCES departments(id)
);

CREATE TABLE time_off_types (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  units TEXT NOT NULL CHECK (units IN ('hours', 'days')),
  color TEXT NOT NULL,
  icon TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('internal', 'remote', 'external'))
);

CREATE TABLE time_off_requests (
  id TEXT PRIMARY KEY,
  employee_id TEXT NOT NULL,
  type_id TEXT NOT NULL,
  start_date TEXT NOT NULL,
  end_date TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('approved', 'denied', 'requested', 'canceled', 'superceded')),
  amount REAL NOT NULL CHECK (amount >= 0),
  unit TEXT NOT NULL CHECK (unit IN ('hours', 'days')),
  employee_note TEXT NOT NULL,
  manager_note TEXT,
  created TEXT NOT NULL,
  updated TEXT NOT NULL,
  last_changed TEXT NOT NULL,
  last_changed_by_user_id TEXT NOT NULL,
  dates_json TEXT NOT NULL,
  FOREIGN KEY (employee_id) REFERENCES employees(id),
  FOREIGN KEY (type_id) REFERENCES time_off_types(id)
);

CREATE INDEX employees_department ON employees(department_id, id);
CREATE INDEX time_off_requests_window ON time_off_requests(start_date, end_date, id);
