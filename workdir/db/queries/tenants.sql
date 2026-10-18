-- name: CreateTenant :one
INSERT INTO tenants (name, api_key, owner_user_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTenantByAPIKey :one
SELECT * FROM tenants
WHERE api_key = $1 AND is_active;

-- name: GetTenantByID :one
SELECT * FROM tenants
WHERE id = $1 AND is_active;

-- name: GetTenantByOwner :one
SELECT * FROM tenants
WHERE owner_user_id = sqlc.arg(owner_user_id)::integer AND is_active;

-- name: RotateTenantAPIKey :one
UPDATE tenants
SET api_key = sqlc.arg(new_key)
WHERE id = sqlc.arg(id) AND is_active
RETURNING *;
