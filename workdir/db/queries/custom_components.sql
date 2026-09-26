-- name: CreateCustomComponent :one
INSERT INTO custom_components (user_id, name, field)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListCustomComponents :many
-- For the builder's "your components" sidebar.
SELECT * FROM custom_components
ORDER BY created_at DESC, id DESC;
