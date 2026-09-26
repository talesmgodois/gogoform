-- name: CreateTenant :one
INSERT INTO tenants (name, api_key)
VALUES (?, ?)
RETURNING *;

-- name: GetTenantByAPIKey :one
SELECT * FROM tenants
WHERE api_key = ? AND is_active;
