-- migrate:up
-- Users sign up and sign in with an email. It is nullable so accounts created
-- before this migration (e.g. the bootstrap admin) stay valid; the application
-- requires it for new signups. Stored lowercase so lookups are case-insensitive.
ALTER TABLE "users"
  ADD COLUMN "email" varchar(254),
  ADD COLUMN "email_verified_at" timestamp NULL,
  ADD CONSTRAINT "users_email_lowercase" CHECK ("email" = lower("email"));

CREATE UNIQUE INDEX "users_email_key" ON "users" (lower("email"));

-- The user owning the tenant (their workspace); NULL for tenants created
-- through the API key flow. A user owns at most one tenant.
ALTER TABLE "tenants"
  ADD COLUMN "owner_user_id" integer NULL REFERENCES "users" ("id");

CREATE UNIQUE INDEX "tenants_owner_user_id_key" ON "tenants" ("owner_user_id");

-- migrate:down
DROP INDEX IF EXISTS "tenants_owner_user_id_key";
ALTER TABLE "tenants" DROP COLUMN IF EXISTS "owner_user_id";
DROP INDEX IF EXISTS "users_email_key";
ALTER TABLE "users"
  DROP CONSTRAINT IF EXISTS "users_email_lowercase",
  DROP COLUMN IF EXISTS "email_verified_at",
  DROP COLUMN IF EXISTS "email";
