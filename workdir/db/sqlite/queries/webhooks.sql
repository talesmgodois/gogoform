-- name: CreateFormWebhook :one
INSERT INTO form_webhooks (form_id, target_url, secret_token)
VALUES (?, ?, ?)
RETURNING *;

-- name: ListActiveWebhooksByForm :many
SELECT * FROM form_webhooks
WHERE form_id = ? AND is_active
ORDER BY id;
