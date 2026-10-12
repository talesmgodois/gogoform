-- name: CreateCustomComponent :one
INSERT INTO custom_components (user_id, name, field_json)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCustomComponentByID :one
SELECT * FROM custom_components
WHERE id = $1;

-- name: ListCustomComponents :many
-- Across every user, so the builder's sidebar shows everyone's saved
-- components.
SELECT * FROM custom_components
ORDER BY created_at DESC, id DESC;
