CREATE TABLE account (
  account_id TEXT PRIMARY KEY,
  account_name TEXT NOT NULL,
  external_account_id TEXT NOT NULL,
  currency_code TEXT NOT NULL,
  plan_name TEXT NOT NULL,
  created_date TEXT NOT NULL
);

CREATE TABLE users (
  user_id TEXT PRIMARY KEY,
  user_name TEXT NOT NULL,
  email TEXT NOT NULL,
  user_status TEXT NOT NULL,
  is_admin TEXT NOT NULL
);

CREATE TABLE envelopes (
  envelope_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  email_subject TEXT NOT NULL,
  email_blurb TEXT NOT NULL,
  created_date_time TEXT NOT NULL,
  sent_date_time TEXT NOT NULL,
  delivered_date_time TEXT NOT NULL,
  completed_date_time TEXT NOT NULL,
  status_changed_date_time TEXT NOT NULL,
  voided_date_time TEXT NOT NULL,
  voided_reason TEXT NOT NULL,
  sender_user_id TEXT NOT NULL
);

CREATE TABLE documents (
  envelope_id TEXT NOT NULL,
  document_id TEXT NOT NULL,
  name TEXT NOT NULL,
  file_extension TEXT NOT NULL,
  doc_order TEXT NOT NULL,
  document_base64 TEXT NOT NULL,
  PRIMARY KEY (envelope_id, document_id),
  FOREIGN KEY (envelope_id) REFERENCES envelopes(envelope_id)
);

CREATE TABLE signers (
  envelope_id TEXT NOT NULL,
  recipient_id TEXT NOT NULL,
  name TEXT NOT NULL,
  email TEXT NOT NULL,
  routing_order TEXT NOT NULL,
  status TEXT NOT NULL,
  sent_date_time TEXT NOT NULL,
  delivered_date_time TEXT NOT NULL,
  signed_date_time TEXT NOT NULL,
  PRIMARY KEY (envelope_id, recipient_id),
  FOREIGN KEY (envelope_id) REFERENCES envelopes(envelope_id)
);

CREATE INDEX documents_envelope ON documents(envelope_id, document_id);
CREATE INDEX signers_envelope ON signers(envelope_id, recipient_id);
