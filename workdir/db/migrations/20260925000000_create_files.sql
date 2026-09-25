-- migrate:up
-- Uploaded files, stored as blobs. The id is a random UUID because files are
-- read publicly through /files/{id}, so it must not be guessable.
CREATE TABLE "files" (
  "id" uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  "tenant_id" integer NOT NULL REFERENCES "tenants" ("id"),
  "name" varchar(255) NOT NULL,
  "content_type" varchar(255) NOT NULL,
  "size_bytes" bigint NOT NULL,
  "checksum_sha256" char(64) NOT NULL,
  "data" bytea NOT NULL,
  "created_at" timestamp NOT NULL DEFAULT (now())
);

CREATE INDEX "files_tenant_id_idx" ON "files" ("tenant_id");

-- migrate:down
DROP TABLE IF EXISTS "files";
