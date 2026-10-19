package handlers

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
)

// appListLimit caps how many forms, files and users the pages list; the
// totals are still shown so truncation is visible.
const appListLimit = 100

// appContentSecurityPolicy lets the pages load the Tailwind Play CDN, which
// injects the generated CSS as inline <style> tags, and run the inline scripts
// carrying the per-request nonce (the %s verb). The file viewer embeds files
// from /files/{id}: images, audio/video, PDFs in a frame and text via fetch;
// the form builder and the form actions call the /forms API. The second %s
// verb is the form-action source list (appLayout.formAction).
const appContentSecurityPolicy = "default-src 'none'; script-src https://cdn.tailwindcss.com 'nonce-%s'; " +
	"style-src 'unsafe-inline'; img-src 'self' data:; media-src 'self'; frame-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action %s; frame-ancestors 'none'"

// defaultFormAction forbids form submissions; the pages call the API with
// fetch instead.
const defaultFormAction = "'none'"

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
	appTemplate               = parseAppPage("app.html")
	appFormTemplate           = parseAppPage("form.html")
	appBuilderTemplate        = parseAppPage("builder.html")
	appSignInTemplate         = parseAppPage("signin.html")
	appAccountTemplate        = parseAppPage("account.html")
	appForgotPasswordTemplate = parseAppPage("forgot_password.html")
	appResetPasswordTemplate  = parseAppPage("reset_password.html")
	appUsersTemplate          = parseAppPage("users.html")
	appMessageTemplate        = parseAppPage("message.html")
	appBillingTemplate        = parseAppPage("billing.html")
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
	// formAction is the form-action source list of the page's
	// Content-Security-Policy; defaultFormAction when empty.
	formAction string
}

func (l *appLayout) layout() *appLayout { return l }

// appPageData is implemented by the page types, which all embed appLayout.
type appPageData interface {
	layout() *appLayout
}

// appView is the scope of a dashboard or form page: the viewer's workspace
// under /app, or every tenant under /app/admin.
type appView struct {
	// Base prefixes the links to the pages of the same view.
	Base string
	// Admin views show the data of every tenant, read-only.
	Admin bool
}

var (
	workspaceView = appView{Base: "/app"}
	adminView     = appView{Base: "/app/admin", Admin: true}
)

// appPage is the data rendered by templates/app.html.
type appPage struct {
	appLayout
	appView
	Forms       []forms.FormOverview
	FormsTotal  int64
	Files       []files.FileSummary
	FilesTotal  int64
	GeneratedAt time.Time
	// Usage is the workspace's usage against its plan; nil in the admin view.
	Usage *usagePanel
}

// usagePanel is the plan usage shown on the workspace dashboard.
type usagePanel struct {
	// Plan is the workspace's plan.
	Plan tenants.Plan
	// Meters are the forms, the submissions this month and the storage.
	Meters []usageMeter
	// Upgrade tells whether to link to /app/billing: billing is enabled and
	// the workspace is on the free plan.
	Upgrade bool
}

// usageMeter is one usage figure against its limit.
type usageMeter struct {
	// Key identifies the meter in the page (data-usage attribute).
	Key, Label string
	// Used and Limit are the rendered figures; Limit is "Unlimited" when the
	// plan sets no limit.
	Used, Limit string
	// Percent is the used share of the limit, capped at 100; 0 when
	// unlimited.
	Percent int
	// Unlimited tells whether the plan sets no limit, so no bar is drawn.
	Unlimited bool
}

// newUsagePanel builds the dashboard usage panel from u.
func newUsagePanel(plan tenants.Plan, u plans.Usage, billingEnabled bool) *usagePanel {
	count := func(n int64) string { return strconv.FormatInt(n, 10) }
	return &usagePanel{
		Plan: plan,
		Meters: []usageMeter{
			newUsageMeter("forms", "Forms", u.Forms, u.Limits.MaxForms, count),
			newUsageMeter("submissions", "Submissions this month", u.SubmissionsThisMonth, u.Limits.MaxSubmissionsPerMonth, count),
			newUsageMeter("storage", "Storage", u.StorageBytes, u.Limits.MaxStorageBytes, formatBytes),
		},
		Upgrade: billingEnabled && plan != tenants.PlanPro,
	}
}

func newUsageMeter(key, label string, used, limit int64, format func(int64) string) usageMeter {
	m := usageMeter{Key: key, Label: label, Used: format(used)}
	if limit == plans.Unlimited {
		m.Limit, m.Unlimited = "Unlimited", true
		return m
	}
	m.Limit = format(limit)
	if limit > 0 {
		m.Percent = int(min(100, used*100/limit))
	} else {
		m.Percent = 100
	}
	return m
}

// appController serves the /app pages: the dashboard listing the forms and
// files of the signed-in user's workspace, the submissions of each form, the
// form builder, the account pages and the admin view of every tenant. Its routes are listed in app_routes.go.
type appController struct {
	tenants     tenants.Repository
	forms       forms.Repository
	files       files.Repository
	submissions submissions.Repository
	auth        *auth.Service
	// plans measures the workspace's usage for the dashboard.
	plans *plans.Enforcer
	// billingEnabled tells whether the /billing routes are mounted, so the
	// billing page can offer checkout and the customer portal.
	billingEnabled bool
	// mounted are the routes registered by mount.
	mounted []appRoute
}

// Index renders the dashboard of the viewer's workspace.
func (c *appController) Index(w http.ResponseWriter, r *http.Request) {
	c.renderIndex(w, r, workspaceView)
}

// AdminIndex renders the dashboard of every tenant.
func (c *appController) AdminIndex(w http.ResponseWriter, r *http.Request) {
	c.renderIndex(w, r, adminView)
}

func (c *appController) renderIndex(w http.ResponseWriter, r *http.Request, v appView) {
	load := c.load
	if v.Admin {
		load = c.loadAll
	}
	page, err := load(r)
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	page.appView, page.Active = v, v.navKey()
	renderAppPage(w, r, appTemplate, &page)
}

// navKey is the navTab highlighted on the pages of v.
func (v appView) navKey() string {
	if v.Admin {
		return "admin"
	}
	return "dashboard"
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
	formAction := l.formAction
	if formAction == "" {
		formAction = defaultFormAction
	}
	h.Set("Content-Security-Policy", fmt.Sprintf(appContentSecurityPolicy, nonce, formAction))
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

// load lists the forms and files of the viewer's tenant.
func (c *appController) load(r *http.Request) (appPage, error) {
	ctx := r.Context()
	t := tenantFrom(ctx)
	page := appPage{GeneratedAt: time.Now().UTC()}
	filter := forms.ListFormsFilter{TenantID: t.ID}
	summaries, err := c.forms.List(ctx, filter, forms.Page{Limit: appListLimit})
	if err != nil {
		return appPage{}, err
	}
	page.Forms = make([]forms.FormOverview, len(summaries))
	for i, f := range summaries {
		page.Forms[i] = forms.FormOverview{FormSummary: f, TenantName: t.Name}
	}
	if page.FormsTotal, err = c.forms.Count(ctx, filter); err != nil {
		return appPage{}, err
	}
	if page.Files, err = c.files.ListByTenant(ctx, t.ID, files.Page{Limit: appListLimit}); err != nil {
		return appPage{}, err
	}
	if page.FilesTotal, err = c.files.CountByTenant(ctx, t.ID); err != nil {
		return appPage{}, err
	}
	usage, err := c.plans.Usage(ctx, t.ID, t.Plan, page.GeneratedAt)
	if err != nil {
		return appPage{}, err
	}
	page.Usage = newUsagePanel(t.Plan, usage, c.billingEnabled)
	return page, nil
}

// loadAll lists the forms and files of every tenant.
func (c *appController) loadAll(r *http.Request) (appPage, error) {
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
