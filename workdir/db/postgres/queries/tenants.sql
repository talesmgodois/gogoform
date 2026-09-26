-- name: CreateTenant :one
INSERT INTO tenants (name, api_key)
VALUES ($1, $2)
RETURNING *;

-- name: GetTenantByAPIKey :one
SELECT * FROM tenants
WHERE api_key = $1 AND is_active;
