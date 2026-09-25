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
