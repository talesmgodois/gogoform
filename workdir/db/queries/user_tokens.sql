-- name: CreateUserToken :one
INSERT INTO user_tokens (user_id, purpose, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ConsumeUserToken :one
-- Atomically marks a token used, but only if it is still valid for the
-- given purpose; returns no row for an unknown, expired, already-used or
-- wrong-purpose token.
UPDATE user_tokens
SET used_at = now()
WHERE token_hash = $1
  AND purpose = $2
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: GetUserTokenByHash :one
SELECT * FROM user_tokens
WHERE token_hash = $1;

-- name: InvalidateUserTokens :exec
-- Run before issuing a new token so older ones for the same purpose can no
-- longer be redeemed.
UPDATE user_tokens
SET used_at = now()
WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL;
