-- migrate:up
-- Mirrors db/postgres/migrations/20260928000000_add_custom_components.sql.
-- Reusable field components saved from the form builder. field_json holds
-- the same field definition shape used inside forms.form_content.
CREATE TABLE custom_components (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) <= 255),
  field_json TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX custom_components_user_id_idx ON custom_components (user_id);

-- migrate:down
DROP INDEX IF EXISTS custom_components_user_id_idx;
DROP TABLE IF EXISTS custom_components;
