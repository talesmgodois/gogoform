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

-- name: GetUserByIdentity :one
-- The user linked to an external OIDC identity, active or not: the caller
-- refuses inactive users rather than mistaking them for unknown identities.
SELECT users.* FROM users
JOIN user_identities ON user_identities.user_id = users.id
WHERE user_identities.issuer = $1 AND user_identities.subject = $2;

-- name: TouchUserIdentity :exec
-- Records a sign-in through an identity and refreshes its email.
UPDATE user_identities
SET last_login_at = now(),
    email = sqlc.narg(email)
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: CreateUserWithIdentity :one
-- Creates a user without a password and links the identity to it, in one
-- statement so neither can exist without the other.
WITH new_user AS (
  INSERT INTO users (username, role)
  VALUES (sqlc.arg(username), sqlc.arg(role))
  RETURNING *
), new_identity AS (
  INSERT INTO user_identities (user_id, issuer, subject, email)
  SELECT id, sqlc.arg(issuer), sqlc.arg(subject), sqlc.narg(email) FROM new_user
)
SELECT * FROM new_user;

-- name: CountUserIdentities :one
SELECT COUNT(*) FROM user_identities
WHERE user_id = $1;
