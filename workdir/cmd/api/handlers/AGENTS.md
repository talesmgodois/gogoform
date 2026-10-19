# handlers — HTTP layer

Loaded only when working in this directory. Root rules in `/AGENTS.md` still apply.

## Where things are
- `routes.go` — `Services`, `NewServices`, `Routes(svc, features, stripe, app)`: every JSON API route, Go 1.22 mux patterns (`"GET /forms/{id}"`).
  Auth wrappers: `auth(...)` = tenant API key or signed-in user (`tenantOrUser`); `guard(policy, h)` = user-only (`authpkg.SignedIn(roles...)`, `authpkg.Public`); `authCtl.PublicForm(h)` = optional credentials.
- `app_routes.go` — `/app` pages as an `[]appRoute{Pattern, Handler, Access, Workspace, Nav}` table; add a page by adding a row.
- `app.go` — page templates (`parseAppPage("x.html")` vars), `appLayout` (Nonce, User, Tabs), `renderAppPage`, formatters.
- `app_*.go` — /app page handlers (auth, billing, builder, forms). `public_page.go` — `/f/{slug}`. `billing.go` — Stripe routes (mounted only when enabled).
- `httputil.go` — `writeJSON`, `decodeJSON`, `pathID`, `parseID`, `parsePage`. Use them; don't hand-roll.
- `templates/` — `layout.html` (shared partials) + one file per page; `static/form-render.js` served at `/static/form-render.js`.

## Rules
- Controllers are structs holding domain `Repository`/services; methods are `http.HandlerFunc`. No SQL or business rules here.
- Errors: `apperrors.WriteHTTP(w, r, err)`; never `http.Error` with ad-hoc text for domain errors.
- JSON endpoints carry swaggo annotations (`// @Summary … // @Router /forms [post]`); run `make swagger` after changing them.
- `/app` pages: CSP with per-request nonce; every inline `<script>` needs `nonce="{{.Nonce}}"`. No inline event handlers (`onclick=`).

## Tests
- Pattern: build fakes → `Routes(svc, testFeatures, stripe, testApp).ServeHTTP(rec, req)` → assert status/body.
- `fakes_test.go` (700 lines) holds `fakeTenants`, `fakeForms`, … — grep `func (f \*fakeForms)` instead of reading it whole. Adding a method to a domain `Repository` means adding it to the fake here.
- Run only this package: `make test PKGS=./cmd/api/handlers/... RUN=TestApp`.

## Big templates
`builder.html` (53 KB, ~870 lines, mostly inline JS) and `app.html` (17 KB): grep for the element id / function name and read only that range.
