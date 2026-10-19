# 000025 plan_limits_and_gates

Depends on 000024 (`tenants.plan`). Single source of truth for plan limits in a new `internal/pkg/plans` package:

| Limit | free | pro |
|---|---|---|
| Forms | 3 | unlimited |
| Submissions per calendar month (all forms) | 100 | 10,000 |
| File storage (sum of files.size_bytes) | 100 MB | 10 GB |
| "Powered by gogoform" badge | shown | hidden |
| Webhooks, CSV/JSON export, email notification on submission | no | yes |

- Enforce on form creation, public submission (count in the current month), and file upload (API and public).
- Exceeded limits return a clear error (HTTP 402 or 403 with an error code like `plan_limit_reached`); the public form page shows "This form is not accepting responses right now" without exposing plan details.
- Gate pro features: webhooks endpoints, exports, and the notification setting.
- `/app` shows usage vs limits (forms, submissions this month, storage) with an upgrade link.
- Wire the badge from 000023 to the plan.

## Tests
- Table-driven tests for every limit on both plans, including the boundary (exactly at the limit).

<!-- RUNNER:PLAN -->
## Plan

1. [x] Create `internal/pkg/plans`: a `Limits` struct (MaxForms, MaxSubmissionsPerMonth, MaxStorageBytes with an "unlimited" value, ShowBadge, Webhooks, Exports, EmailNotifications) and `For(tenants.Plan) Limits` holding the free/pro table, with unknown plans falling back to free. Add pure check helpers `CanCreateForm(limits, formCount)`, `CanSubmit(limits, monthCount)` and `CanStore(limits, usedBytes, newBytes)`, plus `MonthStart(now)` (first day of the calendar month, UTC). Write table-driven tests covering every limit on both plans, including below, exactly at and above each limit.
2. [x] Add two plan error codes to `internal/errors`: `CodePlanLimitReached` ("plan_limit_reached", HTTP 402) and `CodePlanFeatureUnavailable` ("plan_feature_unavailable", HTTP 403). Add constructors `NewPlanLimitReached(msg)` and `NewPlanFeatureUnavailable(msg)`, extend `HTTPStatus`, and add tests.
3. [x] Add the usage queries and repository methods. In `db/queries/submissions.sql`, add `CountSubmissionsByTenantSince(tenant_id, since)`, joining submissions to forms. In `db/queries/files.sql`, add `SumFileSizeByTenant(tenant_id)` (COALESCE to 0). Run `make sqlc`. Expose them as `submissions.Repository.CountByTenantSince` and `files.Repository.StorageUsedByTenant`, then update `cmd/api/handlers/fakes_test.go` and the service tests.
4. [x] Return the tenant's plan with public forms. Select `t.plan` in `GetPublicFormBySlug`, run `make sqlc`, and add `TenantPlan tenants.Plan` to `forms.FormBySlug`, following the `TenantActive` pattern. Map it in `forms_service.go`, update the forms service tests and the handler fakes.
5. [x] Add `plans.Enforcer` in `internal/pkg/plans`. Its small ports are form count (`forms.Repository.Count`), monthly submission count and storage used. Its methods are `CheckFormCreate(ctx, tenantID, plan)`, `CheckSubmission(ctx, tenantID, plan, now)` and `CheckUpload(ctx, tenantID, plan, size)`, each returning a `plan_limit_reached` AppError when over the limit. Add `Usage(ctx, tenantID, plan, now)`, which returns the counts alongside the limits. Write table-driven tests with fake ports on both plans, including the boundaries.
6. [x] Wire the Enforcer into `handlers.Services`/`NewServices` and the controllers in `routes.go`. Enforce the form limit in `formsController.Create` (POST /forms, also used by the /app builder) using the tenant's plan from `tenantFrom`, returning 402 `plan_limit_reached` with a clear upgrade message. Add handler tests: free plan at 2 forms → 201, at 3 → 402; pro plan with many forms → 201.
7. [x] Enforce the monthly submission limit on POST `/public/forms/{slug}/submissions` (`submissionsCtl.Create`) using `FormBySlug.TenantPlan`. Respond 402 `plan_limit_reached` with the generic message "This form is not accepting responses right now." and no plan details. Make the `GET /f/{slug}` page render the existing "Not accepting responses" message page (via `unavailableMessage`/`publicPageController.Form`) when the limit is reached. Add handler and public-page tests at the limit and one below it, for both plans.
8. [x] Enforce the storage limit on POST `/files` (API, tenant plan from context) and POST `/public/forms/{slug}/files` (`FormBySlug.TenantPlan`, generic public message). The check is used + upload size > limit → 402 `plan_limit_reached`. Add handler tests: an upload that lands exactly on the limit succeeds, one byte over is rejected, on both plans.
9. [x] Gate webhooks: POST and GET `/forms/{id}/webhooks` return 403 `plan_feature_unavailable` for tenants whose plan lacks Webhooks. Update the swagger annotations (`@Failure 403`) and add `webhooks_test` cases for free vs pro.
10. [x] Gate the /app exports: `ExportCSV` and `ExportJSON` (workspace view only; admin exports stay ungated) render a 403 message page for plans without Exports. In `templates/form.html`, replace the export buttons with an "Upgrade to Pro" hint linking to `/app/billing` for free tenants. Add `app_forms_test` cases for both plans.
11. [x] Notification gate for the future setting: add a `plans.Allows(plan, feature)` helper (or rely on the `Limits.EmailNotifications` flag, with a test for both plans). Append a note to `__ai_work/tasks/000026_transactional_email.md` saying the per-form notification toggle must check `plans.For(plan).EmailNotifications` and reject free tenants with `plan_feature_unavailable`.
12. [x] Add a usage panel to `/app`. `appController.Index` calls `Enforcer.Usage` for the workspace tenant. `templates/app.html` shows forms, submissions this month and storage (human-readable MB/GB) against their limits, displaying "Unlimited" for pro forms, with progress bars and an upgrade link to `/app/billing` when billing is enabled and the plan is free. Add `app_test` cases for free (with limits shown) and pro rendering.
13. [x] Wire the "Powered by" badge to the plan. Change `showPoweredBy` in `cmd/api/handlers/powered_by.go` to take the tenant's plan and return `plans.For(plan).ShowBadge`, removing the `TODO(000025)`. Pass `FormBySlug.TenantPlan` from `public_page.go` (Form and Thanks), and update `powered_by_test`/`public_page_test` with table cases for free (shown) and pro (hidden).
14. [x] Run `make swagger` to regenerate the docs for the new 402/403 responses and error codes. Run `make test` and `go vet` and fix any failures. Check by hand that `/app` shows usage, and that a free tenant at the submission limit sees "This form is not accepting responses right now" on `/f/{slug}`.
