-- migrate:up
-- is_draft: the form is still being edited. Drafts are never served to the
-- people filling forms in, whatever is_active or the availability window say.
ALTER TABLE "forms"
  ADD COLUMN "is_draft" boolean NOT NULL DEFAULT false;

-- migrate:down
ALTER TABLE "forms" DROP COLUMN IF EXISTS "is_draft";
