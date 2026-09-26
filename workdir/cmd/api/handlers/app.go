package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/customcomponents"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// appListLimit caps how many forms, files and submissions the pages list; the
// totals are still shown so truncation is visible.
const appListLimit = 100

// appContentSecurityPolicy lets the pages load the Tailwind Play CDN, which
// injects the generated CSS as inline <style> tags, and run the inline scripts
// carrying the per-request nonce (the %s verb). The file viewer embeds files
// from /files/{id}: images, audio/video, PDFs in a frame and text via fetch;
// the form builder calls POST /forms.
const appContentSecurityPolicy = "default-src 'none'; script-src https://cdn.tailwindcss.com 'nonce-%s'; " +
	"style-src 'unsafe-inline'; img-src 'self' data:; media-src 'self'; frame-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

//go:embed templates
var templatesFS embed.FS

// appFuncs are the helpers available to every /app template.
var appFuncs = template.FuncMap{
	"bytes":    formatBytes,
	"datetime": formatDateTime,
	"status":   formStatus,
	"access":   formAccess,
	"add":      func(a, b int) int { return a + b },
}

var (
	appTemplate        = parseAppPage("app.html")
	appFormTemplate    = parseAppPage("form.html")
	appBuilderTemplate = parseAppPage("builder.html")
	appSignInTemplate  = parseAppPage("signin.html")
	appAccountTemplate = parseAppPage("account.html")
	appUsersTemplate   = parseAppPage("users.html")
	appMessageTemplate = parseAppPage("message.html")
)

// parseAppPage parses the page template called name along with the shared
// partials of templates/layout.html.
func parseAppPage(name string) *template.Template {
	return template.Must(template.New(name).Funcs(appFuncs).ParseFS(templatesFS, "templates/layout.html", "templates/"+name))
}

// navTab is an entry of the /app navigation bar; see appRoute.Nav.
type navTab struct {
	Key, Label, Href string
}

// appLayout holds the data every /app page shares.
type appLayout struct {
	// Active is the Key of the highlighted navTab.
	Active string
	// Nonce authorizes the page's inline scripts in the Content-Security-Policy.
	Nonce string
	// User is the signed-in viewer; nil when anonymous.
	User *auth.User
	// Tabs are the navigation entries the viewer may open.
	Tabs []navTab
}

func (l *appLayout) layout() *appLayout { return l }

// appPageData is implemented by the page types, which all embed appLayout.
type appPageData interface {
	layout() *appLayout
}

// appPage is the data rendered by templates/app.html.
type appPage struct {
	appLayout
	Forms            []forms.FormOverview
	FormsTotal       int64
	Files            []files.FileSummary
	FilesTotal       int64
	SubmissionsTotal int64
	SubmissionsToday int64
	GeneratedAt      time.Time
}

// appController serves the /app pages: the dashboard listing the forms and
// files of every tenant, the submissions of each form, the form builder and
// the account pages. Its routes are listed in app_routes.go.
type appController struct {
	forms            forms.Repository
	files            files.Repository
	submissions      submissions.Repository
	customComponents customcomponents.Repository
	auth             *auth.Service
	// mounted are the routes registered by mount.
	mounted []appRoute
}

// Index renders the dashboard.
func (c *appController) Index(w http.ResponseWriter, r *http.Request) {
	page, err := c.load(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	page.Active = "dashboard"
	renderAppPage(w, r, appTemplate, &page)
}

// renderAppPage renders t with page, which it gives a fresh nonce and the
// viewer set by guardPage, along with the security headers of every /app page.
func renderAppPage(w http.ResponseWriter, r *http.Request, t *template.Template, page appPageData) {
	renderAppPageStatus(w, r, http.StatusOK, t, page)
}

// renderAppPageStatus is renderAppPage with a custom status code.
func renderAppPageStatus(w http.ResponseWriter, r *http.Request, status int, t *template.Template, page appPageData) {
	nonce, err := newNonce()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	l := page.layout()
	l.Nonce, l.User, l.Tabs = nonce, userFrom(r.Context()), tabsFrom(r.Context())
	// Render to a buffer first so a template error still yields a clean 500.
	var buf bytes.Buffer
	if err := t.Execute(&buf, page); err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", fmt.Sprintf(appContentSecurityPolicy, nonce))
	setAppNoStoreHeaders(h)
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// setAppNoStoreHeaders sets the headers shared by every /app response.
func setAppNoStoreHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (c *appController) load(r *http.Request) (appPage, error) {
	ctx := r.Context()
	page := appPage{GeneratedAt: time.Now().UTC()}
	var err error
	if page.Forms, err = c.forms.ListAll(ctx, forms.Page{Limit: appListLimit}); err != nil {
		return appPage{}, err
	}
	if page.FormsTotal, err = c.forms.CountAll(ctx); err != nil {
		return appPage{}, err
	}
	if page.Files, err = c.files.ListAll(ctx, files.Page{Limit: appListLimit}); err != nil {
		return appPage{}, err
	}
	if page.FilesTotal, err = c.files.CountAll(ctx); err != nil {
		return appPage{}, err
	}
	if page.SubmissionsTotal, err = c.submissions.CountAll(ctx); err != nil {
		return appPage{}, err
	}
	if page.SubmissionsToday, err = c.submissions.CountAllToday(ctx); err != nil {
		return appPage{}, err
	}
	return page, nil
}

// newNonce returns a random CSP nonce. It uses the URL-safe base64 alphabet,
// which CSP accepts, because html/template escapes the "+" of the standard one.
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// basicAuth requires the HTTP Basic credentials of cfg. Both sides are hashed
// before the constant-time comparison so their lengths are not leaked.
func basicAuth(cfg config.AppConfig, next http.HandlerFunc) http.HandlerFunc {
	wantUser, wantPass := sha256.Sum256([]byte(cfg.Username)), sha256.Sum256([]byte(cfg.Password))
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser, gotPass := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="app", charset="UTF-8"`)
			apperrors.WriteHTTP(w, r, apperrors.NewUnauthorized("invalid credentials"))
			return
		}
		next(w, r)
	}
}

// formatBytes renders n as a human-readable size (1 KiB = 1024 B).
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatDateTime renders a UTC timestamp; t may be a time.Time or a
// *time.Time, and nil or zero times render as "—".
func formatDateTime(t any) string {
	var v time.Time
	switch tt := t.(type) {
	case time.Time:
		v = tt
	case *time.Time:
		if tt != nil {
			v = *tt
		}
	}
	if v.IsZero() {
		return "—"
	}
	return v.UTC().Format("2006-01-02 15:04 UTC")
}

// badge is a status label and the Tailwind classes coloring it.
type badge struct {
	Label, Class string
}

// formStatus describes whether f can currently be filled in.
func formStatus(f forms.Form) badge {
	now := time.Now()
	switch {
	case f.IsDraft:
		return badge{"Draft", "bg-indigo-500/10 text-indigo-300 ring-indigo-500/20"}
	case !f.IsActive:
		return badge{"Inactive", "bg-slate-500/10 text-slate-400 ring-slate-500/20"}
	case f.StartDate != nil && now.Before(*f.StartDate):
		return badge{"Scheduled", "bg-amber-500/10 text-amber-400 ring-amber-500/20"}
	case f.EndDate != nil && !now.Before(*f.EndDate):
		return badge{"Ended", "bg-rose-500/10 text-rose-400 ring-rose-500/20"}
	default:
		return badge{"Live", "bg-emerald-500/10 text-emerald-400 ring-emerald-500/20"}
	}
}

// formAccess describes who can fill f in and whether submitters are recorded.
func formAccess(f forms.Form) badge {
	switch {
	case f.PublicAvailable:
		return badge{"Public", "bg-sky-500/10 text-sky-400 ring-sky-500/20"}
	case f.AcceptAnonymous:
		return badge{"Private · anonymous", "bg-violet-500/10 text-violet-400 ring-violet-500/20"}
	default:
		return badge{"Private · identified", "bg-fuchsia-500/10 text-fuchsia-400 ring-fuchsia-500/20"}
	}
}
