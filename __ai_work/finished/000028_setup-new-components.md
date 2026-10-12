# 000028 setup-new-components

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

1. [x] Build a `fakedata` Go package: copy `__ai_work/_inputs/fake.json` into the repo, `go:embed` it, parse the 4 collections (animes, cars, people, cities) into generic records, and add a generic query-param filter helper reusable by all 4 endpoints.
2. [x] Add 4 public REST endpoints (one per collection) that use the `fakedata` package and filter/paginate via query params, following the existing `parsePage`/`writeJSON` pattern from `cmd/api/handlers/httputil.go`; register them in `routes.go`.
3. [x] Add dashboard submission counters: paired Postgres+SQLite SQL queries for total and today's submission counts, domain methods, wire into `appController.load` (`app.go`), and add two stat tiles to `app.html`.
4. [x] Create the `custom_components` table (`id`, `user_id` FK, `name`, `field_json`, `created_at`) via paired Postgres+SQLite dbmate migrations, add paired sqlc queries (create/list/get), regenerate sqlc output, update schema-parity tests.
5. [x] Add `internal/pkg/customcomponents` domain package (ports+service) on the new table, plus `POST /custom-components` and `GET /custom-components` handlers wired through the existing auth guard in `routes.go`.
6. [x] Add the 5 new field types (checkbox, radio-group, image-picker, multi-option select dropdown, star rating) to `builder.html`'s `TYPES` registry and all six switch sites (`newField`, `fieldJSON`, `fromJSON`, `preview`, `typeInputs`, `issues`); base image-picker on the existing `file` type plus an `<img>` preview.
7. [x] Add a "Convert to reusable component" button in the builder's field inspector that opens a confirm/cancel `<dialog>` (mirroring the existing file-preview dialog pattern) and POSTs the field's JSON to the new create-component endpoint.
8. [x] Add a "Saved components" section to the builder's left sidebar, populated via `GET /custom-components`, that inserts a cloned field onto the canvas when clicked.
9. [x] Seed 8 custom components (2 per fake endpoint — one `select-data`, one `autocomplete-data` config pointing at that endpoint) by writing a small one-off script that calls the new create-component endpoint.
10. [x] End-to-end verification: run the app locally, confirm dashboard counters, all 5 new field types, the convert/save/list component flow, the 4 filterable fake-data endpoints, and the 8 seeded components appearing in the builder.
