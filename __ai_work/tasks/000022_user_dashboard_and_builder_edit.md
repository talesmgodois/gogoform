# 000022 user_dashboard_and_builder_edit

Depends on 000021 (users own a tenant). Turn `/app` into the signed-in user's dashboard.

- `/app`: list the user's forms (title, slug, active, submissions count, created at) with actions: open, edit, activate/deactivate, delete (with confirmation), copy public link.
- Builder (`workdir/cmd/api/handlers/templates/builder.html`): uses the session instead of a typed API key; can load an existing form (`/app/builder?form=<id>`) and save with `PUT /forms/{id}`; creating still uses `POST /forms`.
- Form detail page: submissions table with pagination; reuse the existing JSON/CSV export in `app_forms.go`, scoped to the user's tenant.
- Navigation: header with Forms, Account, Billing (placeholder link until 000024), Sign out.
- Empty states and error messages for every page.

## Tests
- Handler tests for list/edit/delete scoped to the signed-in user, including 404 for another user's form id.
