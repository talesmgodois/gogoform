-- name: CreateFormWebhook :one
INSERT INTO form_webhooks (form_id, target_url, secret_token)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListActiveWebhooksByForm :many
SELECT * FROM form_webhooks
WHERE form_id = $1 AND is_active
ORDER BY id;
