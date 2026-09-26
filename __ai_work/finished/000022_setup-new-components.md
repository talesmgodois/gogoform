# 000022 setup-new-components

0. On the home screen, the dashboard should show the total submissions the app has, and how many submissions were made today 


1. You should add the following components

- Check box
- Radio-group
- Image picker - Like file upload, but it should render the image
- Multi option select dropdown
- Rating scale using stars


2. Implement custom components

Every time we customize a component, it should be possible to transform that into a reusable component. Let's say we create a data select with a specific endpoint returning a list of cars. Each reusable component should be stored on a specific table, the field json should be stored on the table
  2.1. The custom components table should have a user_id( the user who created it)
  2.2. The button of the field definition to convert that to a component, should ask if I am sure, with a confirm and cancel button, if saved should appear on the left panel


3. Implement 4 different endpoints on the golang returning fake data, use the __ai_work/_inputs/fake.json, there are 4 collections within the json, create one endpoint for each collection on that json(animes, cars, people and cities) the should allow filtering by any of the fields

4. Insert 8 custom components, 2 for each of the previous endpoints(1 using select data, and the other using autocomplete data)

<!-- RUNNER:PLAN -->
## Plan

1. [x] Add `CountAllSubmissions`/`CountAllSubmissionsToday` sqlc queries in `db/queries/submissions.sql`, regenerate sqlc, add matching `submissions.Repository`/`Service` methods.
2. [x] Wire the two new submission counts into `appController.load` (`cmd/api/handlers/app.go`) and render "Total submissions" / "Submissions today" tiles in `templates/app.html`.
3. [x] Add the `checkbox`, `radio-group`, and `multi-select` field types to `builder.html` (TYPES, newField, fieldJSON, fromJSON, issues, preview, typeInputs).
4. [x] Add the `image-picker` field type to `builder.html`, reusing the `file` type's config but rendering an `<img>` thumbnail preview.
5. [x] Add the `rating` (star scale) field type to `builder.html` with a configurable max-stars property.
6. [x] Add migration `create_custom_components.sql` (`id`, nullable `user_id` FK to `users`, `name`, `field jsonb`, `created_at`) and `db/queries/custom_components.sql` (`CreateCustomComponent`, `ListCustomComponents`); run sqlc generate.
7. [x] Add `internal/pkg/customcomponents` package (domain/ports/service) and wire it into `Services`/`NewServices` in `routes.go`.
8. [x] Add `POST /app/components` and `GET /app/components` JSON endpoints on `appController`, with a new `guardJSON` helper (JSON 401 instead of `guardPage`'s redirect) requiring a signed-in `/app` user.
9. [x] Add a "Save as reusable component" button + confirm/cancel `<dialog>` in `builder.html`'s field inspector, wired to `POST /app/components`.
10. [x] Add a "Your components" sidebar section in `builder.html` that lists saved components (`GET /app/components`) and inserts a field from a saved component's JSON via `fromJSON()` on click/drag.
11. [x] Copy `__ai_work/_inputs/fake.json` into the Go module (e.g. `cmd/api/handlers/fakedata/fake.json`) and load it via `go:embed` into 4 in-memory collections.
12. [x] Add 4 public GET endpoints (`/fake/cars`, `/fake/people`, `/fake/animes`, `/fake/cities`) in `routes.go` sharing one generic per-field, case-insensitive substring filter handler.
13. [x] Add a data-seed migration inserting 8 `custom_components` rows (2 per collection: one `select-data`, one `autocomplete-data`, pointing at the matching `/fake/*` endpoint with appropriate `label_key`/`value_key`/`search_param`).
14. [x] Run `make build`, `make test`, `make migrate-up`, `make sqlc`, and manually verify the dashboard tiles, each new field type in the builder preview, the save-as-component flow, and the `/fake/*` endpoints (with and without filters).
