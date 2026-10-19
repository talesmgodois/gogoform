-- migrate:up
CREATE TYPE "tenant_plan" AS ENUM ('free', 'pro');

-- Billing state of a tenant, kept in sync with Stripe through webhooks. The
-- Stripe columns are NULL until the tenant starts a checkout.
-- subscription_event_at is when Stripe created the last event applied to the
-- stored subscription, so events delivered out of order are dropped.
ALTER TABLE "tenants"
  ADD COLUMN "plan" tenant_plan NOT NULL DEFAULT 'free',
  ADD COLUMN "stripe_customer_id" varchar(255) NULL,
  ADD COLUMN "stripe_subscription_id" varchar(255) NULL,
  ADD COLUMN "subscription_status" varchar(32) NULL,
  ADD COLUMN "current_period_end" timestamp NULL,
  ADD COLUMN "subscription_event_at" timestamp NULL;

CREATE UNIQUE INDEX "tenants_stripe_customer_id_key" ON "tenants" ("stripe_customer_id");

-- Stripe event ids already handled, so webhook retries are processed once.
CREATE TABLE "stripe_events" (
  "id" text PRIMARY KEY,
  "type" text NOT NULL,
  "received_at" timestamp NOT NULL DEFAULT now()
);

-- migrate:down
DROP TABLE IF EXISTS "stripe_events";
DROP INDEX IF EXISTS "tenants_stripe_customer_id_key";
ALTER TABLE "tenants"
  DROP COLUMN IF EXISTS "subscription_event_at",
  DROP COLUMN IF EXISTS "current_period_end",
  DROP COLUMN IF EXISTS "subscription_status",
  DROP COLUMN IF EXISTS "stripe_subscription_id",
  DROP COLUMN IF EXISTS "stripe_customer_id",
  DROP COLUMN IF EXISTS "plan";
DROP TYPE IF EXISTS "tenant_plan";
