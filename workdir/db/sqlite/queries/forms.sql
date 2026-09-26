-- name: CreateForm :one
INSERT INTO forms (tenant_id, title, slug, description, is_active, start_date, end_date, form_content,
                   public_available, accept_anonymous, is_draft)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFormByID :one
-- Scoped by tenant so one tenant can never read another tenant's form.
SELECT f.*, t.name AS tenant_name
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.id = ? AND f.tenant_id = ?;

-- name: GetPublicFormBySlug :one
-- Published forms of active tenants that are active themselves. The caller
-- checks the availability window, so that a form past its end_date can be
-- told apart from one that does not exist, and whether signing in is required
-- (public_available, accept_anonymous).
SELECT f.*
FROM forms f
JOIN tenants t ON t.id = f.tenant_id
WHERE f.slug = ?
  AND f.is_active
  AND NOT f.is_draft
  AND t.is_active;

-- name: ListFormsByTenant :many
-- Optional filters: is_active and is_draft (nil = any) and search
-- (case-insensitive title match, ASCII only on SQLite).
SELECT f.*, (SELECT count(*) FROM form_submissions s WHERE s.form_id = f.id) AS submission_count
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (CAST(sqlc.narg(is_active) AS BOOLEAN) IS NULL OR f.is_active = sqlc.narg(is_active))
  AND (CAST(sqlc.narg(is_draft) AS BOOLEAN) IS NULL OR f.is_draft = sqlc.narg(is_draft))
  AND (CAST(sqlc.narg(search) AS TEXT) IS NULL OR f.title LIKE '%' || sqlc.narg(search) || '%')
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountFormsByTenant :one
-- Same filters as ListFormsByTenant, for pagination totals.
SELECT count(*)
FROM forms f
WHERE f.tenant_id = sqlc.arg(tenant_id)
  AND (CAST(sqlc.narg(is_active) AS BOOLEAN) IS NULL OR f.is_active = sqlc.narg(is_active))
  AND (CAST(sqlc.narg(is_draft) AS BOOLEAN) IS NULL OR f.is_draft = sqlc.narg(is_draft))
  AND (CAST(sqlc.narg(search) AS TEXT) IS NULL OR f.title LIKE '%' || sqlc.narg(search) || '%');

-- name: UpdateForm :one
-- Once a form has submissions its content is locked and it cannot become a
-- draft again: no row is updated then, as when the form does not exist.
-- form_content_check and is_draft_check repeat the form_content/is_draft
-- values passed to the SET clause: sqlc's SQLite query rewriter does not
-- dedupe repeated sqlc.arg() references (unlike the pgx engine), and it
-- drops named args used as a bare negated boolean (NOT sqlc.arg(x)), so the
-- draft-lock check below uses its own args and a CAST(...) = 0 comparison.
UPDATE forms
SET title = sqlc.arg(title),
    slug = sqlc.arg(slug),
    description = sqlc.arg(description),
    is_active = sqlc.arg(is_active),
    start_date = sqlc.arg(start_date),
    end_date = sqlc.arg(end_date),
    form_content = sqlc.arg(form_content),
    public_available = sqlc.arg(public_available),
    accept_anonymous = sqlc.arg(accept_anonymous),
    is_draft = sqlc.arg(is_draft),
    updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id)
  AND (NOT EXISTS (SELECT 1 FROM form_submissions s WHERE s.form_id = forms.id)
       OR (json(forms.form_content) = json(CAST(sqlc.arg(form_content_check) AS TEXT))
           AND (CAST(sqlc.arg(is_draft_check) AS BOOLEAN) = 0 OR forms.is_draft)))
RETURNING *;

-- name: DeleteForm :execrows
DELETE FROM forms
WHERE id = ? AND tenant_id = ?;

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
WHERE f.id = ?;
