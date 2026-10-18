# 000023 public_form_page_and_embed

Respondents have no HTML page today: `GET /public/forms/{slug}` only returns JSON.

- `GET /f/{slug}`: server-rendered page that renders `form_content` with every field type the builder supports (textfield variants, textarea, select, select-data, autocomplete-data, file, toggle, panel/content/button). Reuse the builder's rendering logic instead of duplicating it.
- Submit to `POST /public/forms/{slug}/submissions`, then show a thank-you page. Respect `is_active`, `start_date`/`end_date` and private forms (signin required) with clear messages.
- Validate the submission payload against the form definition (required fields, types, allowed options, unknown keys rejected) and return field-level errors.
- File fields: respondents can upload without an API key through a public upload endpoint bound to the form (e.g. `POST /public/forms/{slug}/files`), honoring the 10 MB limit and the field's accepted types.
- Embed: `GET /f/{slug}?embed=1` with a minimal layout and `frame-ancestors *` for that route only; show the iframe snippet on the form detail page in `/app`.
- "Powered by gogoform" footer badge, controlled by a function that will later read the plan (000025); for now always shown.

## Tests
- Payload validation unit tests per field type; handler tests for inactive/expired/private forms.
