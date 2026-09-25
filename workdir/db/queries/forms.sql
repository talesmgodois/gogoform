-- name: CreateForm :one
INSERT INTO forms (tenant_id, title, slug, description, is_active, start_date, end_date, form_content,
                   public_available, accept_anonymous, is_draft)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetFormByID :one
-- Scoped by tenant so one tenant can never read another tenant's form.
SELECT f.*, t.name AS tenant_name
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.id = $1 AND f.tenant_id = $2;

-- name: GetPublicFormBySlug :one
-- Published forms of active tenants that are active themselves. The caller
-- checks the availability window, so that a form past its end_date can be
-- told apart from one that does not exist, and whether signing in is required
-- (public_available, accept_anonymous).
SELECT f.*
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.slug = $1
  AND f.is_active
  AND NOT f.is_draft
  AND t.is_active;

-- name: ListFormsByTenant :many
-- Optional filters: is_active and is_draft (nil = any) and search
-- (case-insensitive title match).
SELECT f.*, (SELECT count(*) FROM form_submissions s WHERE s.form_id = f.id) AS submission_count
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(is_active)::boolean IS NULL OR f.is_active = sqlc.narg(is_active))
  AND (sqlc.narg(is_draft)::boolean IS NULL OR f.is_draft = sqlc.narg(is_draft))
  AND (sqlc.narg(search)::text IS NULL OR f.title ILIKE '%' || sqlc.narg(search) || '%')
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountFormsByTenant :one
-- Same filters as ListFormsByTenant, for pagination totals.
SELECT count(*)
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(is_active)::boolean IS NULL OR f.is_active = sqlc.narg(is_active))
  AND (sqlc.narg(is_draft)::boolean IS NULL OR f.is_draft = sqlc.narg(is_draft))
  AND (sqlc.narg(search)::text IS NULL OR f.title ILIKE '%' || sqlc.narg(search) || '%');

-- name: UpdateForm :one
UPDATE forms
SET title = $3,
    slug = $4,
    description = $5,
    is_active = $6,
    start_date = $7,
    end_date = $8,
    form_content = $9,
    public_available = $10,
    accept_anonymous = $11,
    is_draft = $12,
    updated_at = now()
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: DeleteForm :execrows
DELETE FROM forms
WHERE id = $1 AND tenant_id = $2;

-- name: ListAllForms :many
-- Across every tenant, for the read-only /app dashboard.
SELECT f.*, t.name AS tenant_name,
       (SELECT count(*) FROM form_submissions s WHERE s.form_id = f.id) AS submission_count
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAllForms :one
SELECT count(*) FROM forms;

-- name: GetAnyFormByID :one
-- Across every tenant, for the read-only /app dashboard.
SELECT f.*, t.name AS tenant_name,
       (SELECT count(*) FROM form_submissions s WHERE s.form_id = f.id) AS submission_count
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.id = $1;
