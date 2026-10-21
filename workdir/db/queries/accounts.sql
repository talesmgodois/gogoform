-- Account deletion (internal/pkg/accounts). The foreign keys of the tenant
-- data have no ON DELETE CASCADE, so the rows are deleted child first, in
-- one transaction.

-- name: DeleteSubmissionMetadataByTenant :exec
DELETE FROM submission_metadata sm
USING form_submissions s, forms f
WHERE sm.submission_id = s.id AND s.form_id = f.id AND f.tenant_id = $1;

-- name: DeleteSubmissionsByTenant :exec
DELETE FROM form_submissions s
USING forms f
WHERE s.form_id = f.id AND f.tenant_id = $1;

-- name: DeleteWebhooksByTenant :exec
DELETE FROM form_webhooks w
USING forms f
WHERE w.form_id = f.id AND f.tenant_id = $1;

-- name: DeleteFormsByTenant :exec
DELETE FROM forms
WHERE tenant_id = $1;

-- name: DeleteFilesByTenant :exec
-- Files are database blobs: deleting the rows deletes their content.
DELETE FROM files
WHERE tenant_id = $1;

-- name: DeleteTenant :exec
DELETE FROM tenants
WHERE id = $1;

-- name: ClearSubmissionsUser :exec
-- Submissions the user sent to other tenants' forms stay with their owners,
-- as anonymous submissions.
UPDATE form_submissions
SET user_id = NULL
WHERE user_id = $1;

-- name: DeleteUser :exec
-- The user's tokens go with it (user_tokens ON DELETE CASCADE).
DELETE FROM users
WHERE id = $1;
