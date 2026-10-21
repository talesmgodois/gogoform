package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHelpResolve(t *testing.T) {
	tests := []struct {
		path  string
		want  string
		exact bool
	}{
		{"/", "index", true},
		{"", "index", true},
		{"/pricing", "pricing", true},
		{"/app", "app/index", true},
		{"/app/", "app/index", true},
		{"/app/account", "app/account", true},
		{"/app/forms/42", "app/forms/_id", true},
		{"/app/admin", "app/admin/index", true},
		{"/app/admin/forms/7", "app/admin/forms/_id", true},
		{"/app/forgot-password", "app/forgot-password", true},
		{"/app/forms/42/submissions.csv", "index", false},
		{"/nope", "index", false},
		{"/app/../app/account", "index", false},
		{"/app/forms/..", "index", false},
		{"/app/./account", "index", false},
		{`/app\account`, "index", false},
	}
	for _, tt := range tests {
		doc, exact := helpDocs.resolve(tt.path)
		if doc.Key != tt.want || exact != tt.exact {
			t.Errorf("resolve(%q) = %q, %v; want %q, %v", tt.path, doc.Key, exact, tt.want, tt.exact)
		}
	}
}

func TestHelpFragment(t *testing.T) {
	tests := []struct {
		path, doc, want string
	}{
		{"/app/help/app/account", "app/account", `id="help-change-email"`},
		{"/app/help/app/forms/9", "app/forms/_id", `id="help-submissions"`},
		{"/app/help/app/help", "app/help", `id="help-help-panel"`},
		{"/app/help/", "index", "GoGoForm manual"},
		{"/app/help/nope/at/all", "index", "GoGoForm manual"},
	}
	svc := testServices()
	for _, tt := range tests {
		rec := serve(svc, httptest.NewRequest(http.MethodGet, tt.path, nil))
		assertStatus(t, rec, http.StatusOK)
		if got := rec.Header().Get("X-Help-Doc"); got != tt.doc {
			t.Errorf("%s: X-Help-Doc = %q, want %q", tt.path, got, tt.doc)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q", tt.path, ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, tt.want) || strings.Contains(body, "<html") {
			t.Errorf("%s: body = %s, want a fragment containing %q", tt.path, body, tt.want)
		}
	}
}

func TestHelpManualPage(t *testing.T) {
	svc := testServices()

	rec := serve(svc, httptest.NewRequest(http.MethodGet, "/app/help", nil))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, `data-help-article="index"`) {
		t.Error("manual does not show the overview by default")
	}
	for key := range helpDocs.docs {
		if !strings.Contains(body, `data-help-doc="`+key+`"`) {
			t.Errorf("table of contents misses %s", key)
		}
	}

	rec = serve(svc, httptest.NewRequest(http.MethodGet, "/app/help?doc=app/billing", nil))
	assertStatus(t, rec, http.StatusOK)
	body = rec.Body.String()
	for _, want := range []string{`data-help-article="app/billing"`, `id="help-manage-subscription"`, `aria-current="page">Billing</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("billing doc does not contain %q", want)
		}
	}

	rec = serve(svc, httptest.NewRequest(http.MethodGet, "/app/help?doc=../templates/app", nil))
	assertStatus(t, rec, http.StatusNotFound)
}

func TestHelpRendersMarkdownSafely(t *testing.T) {
	lib, err := loadHelp(fstest.MapFS{
		"help/index.md": {Data: []byte("---\nsection: Start\norder: 3\n---\n" +
			"# Tom & <b>Jerry</b>\n\n<script>alert(1)</script>\n\n[click](javascript:alert(1))\n\n## Two words {#custom}\n\n## Auto id\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := lib.docs["index"]
	html := string(doc.HTML)
	for _, banned := range []string{"<script", "javascript:", "<b>"} {
		if strings.Contains(html, banned) {
			t.Errorf("rendered doc contains %q: %s", banned, html)
		}
	}
	for _, want := range []string{"Tom &amp;", `id="help-custom"`, `id="help-auto-id"`} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered doc does not contain %q: %s", want, html)
		}
	}
	if doc.Section != "Start" || doc.Order != 3 || !doc.HasAnchor("custom") || doc.HasAnchor("help-custom") {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Title != "Tom & Jerry" {
		t.Errorf("Title = %q", doc.Title)
	}
}

func TestHelpLoadRejectsBrokenDocs(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"missing index":     {"help/app.md": {Data: []byte("# App\n")}},
		"no title":          {"help/index.md": {Data: []byte("## Only a section\n")}},
		"open front matter": {"help/index.md": {Data: []byte("---\norder: 1\n# Title\n")}},
		"bad order":         {"help/index.md": {Data: []byte("---\norder: first\n---\n# Title\n")}},
	}
	for name, fsys := range tests {
		if _, err := loadHelp(fsys); err == nil {
			t.Errorf("%s: loadHelp succeeded", name)
		}
	}
}

// TestEveryPageHasHelp checks that each /app page and product page has a
// manual page of its own, so the help sidenav never falls back to the
// overview.
func TestEveryPageHasHelp(t *testing.T) {
	pages := []string{"/", "/pricing", "/terms", "/privacy"}
	c := &appController{limiter: &rateLimiters{}}
	for _, rt := range c.routes() {
		method, p, _ := strings.Cut(rt.Pattern, " ")
		if method != http.MethodGet || strings.Contains(p, "...}") || path.Ext(p) != "" {
			continue
		}
		pages = append(pages, strings.NewReplacer("{id}", "1").Replace(p))
	}
	for _, p := range pages {
		if doc, exact := helpDocs.resolve(p); !exact {
			t.Errorf("%s has no help doc (falls back to %s)", p, doc.Key)
		}
	}
}

// helpIconDocs maps the templates using helpIcon to the docs of the pages
// rendering them; an icon's anchor must be a heading of one of them.
var helpIconDocs = map[string][]string{
	"landing.html":         {"index"},
	"pricing.html":         {"pricing"},
	"terms.html":           {"terms"},
	"privacy.html":         {"privacy"},
	"app.html":             {"app/index", "app/admin/index"},
	"form.html":            {"app/forms/_id", "app/admin/forms/_id"},
	"builder.html":         {"app/builder"},
	"account.html":         {"app/account"},
	"billing.html":         {"app/billing"},
	"users.html":           {"app/users"},
	"signin.html":          {"app/signin"},
	"forgot_password.html": {"app/forgot-password"},
	"reset_password.html":  {"app/reset-password"},
}

func TestHelpIconsLinkToDocHeadings(t *testing.T) {
	icon := regexp.MustCompile(`helpIcon "([^"]*)"`)
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := fs.ReadFile(templatesFS, p)
		if err != nil {
			return err
		}
		name := path.Base(p)
		for _, m := range icon.FindAllStringSubmatch(string(b), -1) {
			keys, ok := helpIconDocs[name]
			if !ok {
				t.Errorf("%s uses helpIcon but is not in helpIconDocs", name)
				break
			}
			found := false
			for _, key := range keys {
				if doc, ok := helpDocs.get(key); ok && doc.HasAnchor(m[1]) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: helpIcon %q matches no heading of %v", name, m[1], keys)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPagesHaveHelpSidenav(t *testing.T) {
	svc := testServices()
	for _, p := range []string{"/", "/pricing", "/app/signin", "/app/help"} {
		body := serve(svc, httptest.NewRequest(http.MethodGet, p, nil)).Body.String()
		for _, want := range []string{`id="help-drawer"`, `data-help=""`, `fetch("/app/help" + location.pathname`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q", p, want)
			}
		}
	}
	body := serve(svc, httptest.NewRequest(http.MethodGet, "/pricing", nil)).Body.String()
	if !strings.Contains(body, `data-help="plans"`) {
		t.Error("/pricing has no help icon for the plans")
	}
}
