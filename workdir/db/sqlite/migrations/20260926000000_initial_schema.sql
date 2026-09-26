-- migrate:up
-- Squashed schema mirroring the PostgreSQL schema through
-- db/postgres/migrations/20260925160000_add_form_drafts.sql (see
-- docs/architecture/multi-database.md). SQLite starts as one migration
-- because it cannot replay PostgreSQL's ALTER TABLE ADD CONSTRAINT history;
-- from here on, both engines get new migrations in pairs.

CREATE TABLE tenants (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(name) <= 100),
  api_key TEXT UNIQUE NOT NULL CHECK (length(api_key) <= 100),
  is_active BOOLEAN DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Accounts signing in with a username and password. The role grants access to
-- the protected routes; new accounts are BASIC.
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  -- Stored lowercase so sign-in is case-insensitive.
  username TEXT UNIQUE NOT NULL CHECK (length(username) <= 50 AND username = lower(username)),
  password_hash TEXT NOT NULL CHECK (length(password_hash) <= 255),
  role TEXT NOT NULL DEFAULT 'BASIC' CHECK (role IN ('ADMIN', 'FORM_CREATOR', 'BASIC')),
  is_active BOOLEAN NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE forms (
  id INTEGER PRIMARY KEY,
  tenant_id INTEGER NOT NULL REFERENCES tenants (id),
  title TEXT NOT NULL CHECK (length(title) <= 255),
  slug TEXT UNIQUE NOT NULL CHECK (length(slug) <= 255),
  description TEXT,
  is_active BOOLEAN DEFAULT 1,
  start_date DATETIME,
  end_date DATETIME,
  form_content TEXT NOT NULL CHECK (json_valid(form_content)),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  -- public_available: the form can be read and filled in without signing in.
  -- accept_anonymous: submissions are stored without the submitter's identity.
  -- A public form cannot require an identity, since anyone may fill it in.
  public_available BOOLEAN NOT NULL DEFAULT 1,
  accept_anonymous BOOLEAN NOT NULL DEFAULT 1,
  -- is_draft: the form is still being edited. Drafts are never served to the
  -- people filling forms in, whatever is_active or the availability window say.
  is_draft BOOLEAN NOT NULL DEFAULT 0,
  CONSTRAINT forms_public_accepts_anonymous CHECK (NOT public_available OR accept_anonymous)
);

CREATE TABLE form_submissions (
  id INTEGER PRIMARY KEY,
  form_id INTEGER NOT NULL REFERENCES forms (id),
  payload TEXT NOT NULL CHECK (json_valid(payload)),
  submitted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  -- The signed-in submitter; NULL for anonymous submissions.
  user_id INTEGER REFERENCES users (id)
);

CREATE INDEX form_submissions_user_id_idx ON form_submissions (user_id);

CREATE TABLE submission_metadata (
  id INTEGER PRIMARY KEY,
  submission_id INTEGER UNIQUE NOT NULL REFERENCES form_submissions (id),
  ip_address TEXT CHECK (length(ip_address) <= 45),
  user_agent TEXT,
  completion_time_seconds INTEGER,
  referer TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE form_webhooks (
  id INTEGER PRIMARY KEY,
  form_id INTEGER NOT NULL REFERENCES forms (id),
  target_url TEXT NOT NULL,
  secret_token TEXT CHECK (length(secret_token) <= 100),
  is_active BOOLEAN DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Uploaded files, stored as blobs. The id is a random UUID because files are
-- read publicly through /files/{id}, so it must not be guessable. Generated
-- by the application (crypto/rand v4), not the database.
CREATE TABLE files (
  id TEXT PRIMARY KEY NOT NULL,
  tenant_id INTEGER NOT NULL REFERENCES tenants (id),
  name TEXT NOT NULL CHECK (length(name) <= 255),
  content_type TEXT NOT NULL CHECK (length(content_type) <= 255),
  size_bytes INTEGER NOT NULL,
  checksum_sha256 TEXT NOT NULL CHECK (length(checksum_sha256) = 64),
  data BLOB NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX files_tenant_id_idx ON files (tenant_id);

-- migrate:down
DROP INDEX IF EXISTS files_tenant_id_idx;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS form_webhooks;
DROP TABLE IF EXISTS submission_metadata;
DROP INDEX IF EXISTS form_submissions_user_id_idx;
DROP TABLE IF EXISTS form_submissions;
DROP TABLE IF EXISTS forms;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
