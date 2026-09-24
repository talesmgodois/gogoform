-- name: CreateForm :one
INSERT INTO forms (tenant_id, title, slug, description, is_active, start_date, end_date, form_content)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetFormByID :one
-- Scoped by tenant so one tenant can never read another tenant's form.
SELECT f.*, t.name AS tenant_name
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.id = $1 AND f.tenant_id = $2;

-- name: GetPublicFormBySlug :one
-- Public access: the form and its tenant must be active and inside the availability window.
SELECT f.*
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.slug = $1
  AND f.is_active
  AND t.is_active
  AND (f.start_date IS NULL OR f.start_date <= now())
  AND (f.end_date IS NULL OR f.end_date > now());

-- name: ListFormsByTenant :many
-- Optional filters: is_active (nil = any) and search (case-insensitive title match).
SELECT f.*, (SELECT count(*) FROM form_submissions s WHERE s.form_id = f.id) AS submission_count
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(is_active)::boolean IS NULL OR f.is_active = sqlc.narg(is_active))
  AND (sqlc.narg(search)::text IS NULL OR f.title ILIKE '%' || sqlc.narg(search) || '%')
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountFormsByTenant :one
-- Same filters as ListFormsByTenant, for pagination totals.
SELECT count(*)
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(is_active)::boolean IS NULL OR f.is_active = sqlc.narg(is_active))
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
    updated_at = now()
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: DeleteForm :execrows
DELETE FROM forms
WHERE id = $1 AND tenant_id = $2;
