-- name: CreateUser :one
INSERT INTO users (username, password_hash, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateUserIfNotExists :execrows
-- For bootstrapping accounts: an existing user is left untouched.
INSERT INTO users (username, password_hash, role)
VALUES ($1, $2, $3)
ON CONFLICT (username) DO NOTHING;

-- name: GetUserByUsername :one
SELECT * FROM users
WHERE username = $1 AND is_active;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND is_active;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateUserRole :one
UPDATE users
SET role = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;
