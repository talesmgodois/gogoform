-- name: CreateFile :one
-- Returns the metadata only: the blob was just sent by the caller.
INSERT INTO files (tenant_id, name, content_type, size_bytes, checksum_sha256, data)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, tenant_id, name, content_type, size_bytes, checksum_sha256, created_at;

-- name: GetFileByID :one
SELECT * FROM files
WHERE id = $1;

-- name: DeleteFile :execrows
-- Scoped by tenant so one tenant can never remove another tenant's file.
DELETE FROM files
WHERE id = $1 AND tenant_id = $2;

-- name: ListAllFiles :many
-- Metadata only, across every tenant, for the read-only /app dashboard.
SELECT f.id, f.tenant_id, f.name, f.content_type, f.size_bytes, f.checksum_sha256, f.created_at,
       t.name AS tenant_name
FROM files f
JOIN tenants t ON t.id = f.tenant_id
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAllFiles :one
SELECT count(*) FROM files;
