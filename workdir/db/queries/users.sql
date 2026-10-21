-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateUserIfNotExists :execrows
-- For bootstrapping accounts: an existing user is left untouched.
INSERT INTO users (username, password_hash, role)
VALUES ($1, $2, $3)
ON CONFLICT (username) DO NOTHING;

-- name: GetUserByUsername :one
SELECT * FROM users
WHERE username = $1 AND is_active;

-- name: GetUserByEmail :one
-- Emails are stored lowercase; lower() matches the users_email_key index.
SELECT * FROM users
WHERE lower(email) = lower(sqlc.arg(email)::text) AND is_active;

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

-- name: MarkUserEmailVerified :one
UPDATE users
SET email_verified_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserEmail :one
-- A changed email must be verified again.
UPDATE users
SET email = $2,
    email_verified_at = NULL,
    updated_at = now()
WHERE id = $1 AND is_active
RETURNING *;
