# 000030 implement-interactive-menu

Implement a help page, This is going to be the system manual, you should have help icons on every page, and use a sidenav to the right to show the help

add help icons to explain important features on the screen

Use markdown to generate the help pages, renderize them, basically the current pagt url should match the markdown file doc

<!-- RUNNER:PLAN -->
## Plan

1. [x] Help content layer: add `github.com/yuin/goldmark` to `go.mod`. Create an embedded `help/` markdown tree in `cmd/api/handlers` that mirrors the page URLs. For example, `/app` maps to `help/app/index.md`, `/app/forms/{id}` maps to `help/app/forms/_id.md` and `/app/account` maps to `help/app/account.md`, with anchored sections for each feature. Write a `help.go` resolver that turns a request path into a doc: path params match the `_param` files, there is a fallback to `help/index.md`, and `..` traversal is rejected. It renders the doc to safe HTML with goldmark, raw HTML disabled, and caches the result at startup.
2. [x] Help handlers and routes: add `GET /app/help/{path...}` with Public access to the `app_routes.go` table. It returns the rendered HTML fragment for the sidenav, so a page can ask for its own doc by URL. Add a full standalone `/app/help` manual page (new `help.html` template) with a table of contents built from the markdown tree. Do the same for the public site pages through `siteController`.
3. [x] Layout and UI: in `templates/layout.html`, add a right-hand sidenav partial (slide-in drawer with a close button) and a global "?" help button in the app and site top bars. Add a reusable `helpIcon "anchor"` template func in `appFuncs`. Write a small nonce'd script that loads the current page's help fragment into the sidenav and scrolls to the anchor of a clicked icon, with no inline handlers or styles. Add the Tailwind classes and run `make css`.
4. [x] Contextual help icons and manual content: place help icons next to the important features on every page. That means the forms list, form detail and submissions with exports, the builder (fields, settings, webhooks, API key), account (email, password, delete), billing/plans, admin, users, sign-in/verify/password reset, and the public site pages. Write the matching markdown for every page, with anchors that match the icons.
5. [x] Tests and docs: add `help_test.go` covering the path-to-file mapping (static paths, `{id}` params, fallback, traversal), the fragment and manual endpoints, and the HTML escaping of markdown. Add a test that every `appRoute` GET page and site page has a help doc, and that every `helpIcon` anchor exists in its doc. Make sure `TestTemplatesHaveNoInlineStyles` still passes. Update `cmd/api/handlers/AGENTS.md` to explain how to add help for a new page. Run `make test` and check that the app actually runs.
