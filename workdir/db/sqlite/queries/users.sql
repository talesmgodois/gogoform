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

-- name: GetUserByIdentity :one
-- The user linked to an external OIDC identity, active or not: the caller
-- refuses inactive users rather than mistaking them for unknown identities.
SELECT users.* FROM users
JOIN user_identities ON user_identities.user_id = users.id
WHERE user_identities.issuer = ? AND user_identities.subject = ?;

-- name: TouchUserIdentity :exec
-- Records a sign-in through an identity and refreshes its email.
UPDATE user_identities
SET last_login_at = CURRENT_TIMESTAMP,
    email = sqlc.narg(email)
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: CreateUserWithoutPassword :one
-- First half of creating an OIDC user: SQLite has no data-modifying CTEs,
-- so the adapter runs this and CreateUserIdentity in one transaction.
INSERT INTO users (username, role)
VALUES (?, ?)
RETURNING *;

-- name: CreateUserIdentity :exec
INSERT INTO user_identities (user_id, issuer, subject, email)
VALUES (?, ?, ?, ?);

-- name: CountUserIdentities :one
SELECT COUNT(*) FROM user_identities
WHERE user_id = ?;
