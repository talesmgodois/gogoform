-- name: CreateUser :one
INSERT INTO users (username, password_hash, role)
VALUES (?, ?, ?)
RETURNING *;

-- name: CreateUserIfNotExists :execrows
-- For bootstrapping accounts: an existing user is left untouched.
INSERT INTO users (username, password_hash, role)
VALUES (?, ?, ?)
ON CONFLICT (username) DO NOTHING;

-- name: GetUserByUsername :one
SELECT * FROM users
WHERE username = ? AND is_active;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = ? AND is_active;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateUserRole :one
UPDATE users
SET role = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;
