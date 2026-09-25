-- name: CreateFormSubmission :one
INSERT INTO form_submissions (form_id, payload)
VALUES ($1, $2)
RETURNING *;

-- name: CreateSubmissionMetadata :one
INSERT INTO submission_metadata (submission_id, ip_address, user_agent, completion_time_seconds, referer)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSubmissionsByForm :many
-- Scoped by tenant through the owning form; metadata is optional (LEFT JOIN).
SELECT s.*, m.ip_address, m.user_agent, m.completion_time_seconds, m.referer
FROM form_submissions s
JOIN forms f ON f.id = s.form_id
LEFT JOIN submission_metadata m ON m.submission_id = s.id
WHERE s.form_id = sqlc.arg(form_id) AND f.tenant_id = sqlc.arg(tenant_id)
ORDER BY s.submitted_at DESC, s.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListAllSubmissionsByForm :many
-- Same as ListSubmissionsByForm without pagination, for exports.
SELECT s.*, m.ip_address, m.user_agent, m.completion_time_seconds, m.referer
FROM form_submissions s
JOIN forms f ON f.id = s.form_id
LEFT JOIN submission_metadata m ON m.submission_id = s.id
WHERE s.form_id = sqlc.arg(form_id) AND f.tenant_id = sqlc.arg(tenant_id)
ORDER BY s.submitted_at DESC, s.id DESC;
