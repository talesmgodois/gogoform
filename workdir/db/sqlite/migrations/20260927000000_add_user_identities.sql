-- migrate:up transaction:false
-- Mirrors db/postgres/migrations/20260927000000_add_user_identities.sql.
-- SQLite cannot drop NOT NULL from a column, so users is rebuilt. Foreign
-- keys must be off while it is (form_submissions references it), and that
-- pragma is a no-op inside a transaction: hence transaction:false and the
-- explicit BEGIN/COMMIT.
PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TABLE users_new (
  id INTEGER PRIMARY KEY,
  -- Stored lowercase so sign-in is case-insensitive.
  username TEXT UNIQUE NOT NULL CHECK (length(username) <= 50 AND username = lower(username)),
  -- NULL for users created through OIDC sign-in, who have no password.
  password_hash TEXT CHECK (length(password_hash) <= 255),
  role TEXT NOT NULL DEFAULT 'BASIC' CHECK (role IN ('ADMIN', 'FORM_CREATOR', 'BASIC')),
  is_active BOOLEAN NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO users_new (id, username, password_hash, role, is_active, created_at, updated_at)
SELECT id, username, password_hash, role, is_active, created_at, updated_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

-- External OpenID Connect identities linked to a user. (issuer, subject) is
-- the stable identifier of a person at a provider; the email is informative
-- only and never used to find or link accounts.
CREATE TABLE user_identities (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  issuer TEXT NOT NULL CHECK (length(issuer) <= 255),
  subject TEXT NOT NULL CHECK (length(subject) <= 255),
  email TEXT CHECK (length(email) <= 320),
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (issuer, subject)
);

CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

COMMIT;
PRAGMA foreign_keys = ON;

-- migrate:down transaction:false
PRAGMA foreign_keys = OFF;
BEGIN;

DROP TABLE IF EXISTS user_identities;

-- Users without a password get an unusable hash, so NOT NULL can come back.
CREATE TABLE users_old (
  id INTEGER PRIMARY KEY,
  username TEXT UNIQUE NOT NULL CHECK (length(username) <= 50 AND username = lower(username)),
  password_hash TEXT NOT NULL CHECK (length(password_hash) <= 255),
  role TEXT NOT NULL DEFAULT 'BASIC' CHECK (role IN ('ADMIN', 'FORM_CREATOR', 'BASIC')),
  is_active BOOLEAN NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO users_old (id, username, password_hash, role, is_active, created_at, updated_at)
SELECT id, username, COALESCE(password_hash, '!'), role, is_active, created_at, updated_at FROM users;
DROP TABLE users;
ALTER TABLE users_old RENAME TO users;

COMMIT;
PRAGMA foreign_keys = ON;
