package handlers

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
)

// appListLimit caps how many forms and files the dashboard lists; the totals
// are still shown so truncation is visible.
const appListLimit = 100

// appContentSecurityPolicy only lets the page load the Tailwind Play CDN,
// which injects the generated CSS as inline <style> tags.
const appContentSecurityPolicy = "default-src 'none'; script-src https://cdn.tailwindcss.com; " +
	"style-src 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

//go:embed templates
var templatesFS embed.FS

var appTemplate = template.Must(template.New("app.html").Funcs(template.FuncMap{
	"bytes":    formatBytes,
	"datetime": formatDateTime,
	"status":   formStatus,
}).ParseFS(templatesFS, "templates/app.html"))

// appPage is the data rendered by templates/app.html.
type appPage struct {
	Forms       []forms.FormOverview
	FormsTotal  int64
	Files       []files.FileSummary
	FilesTotal  int64
	GeneratedAt time.Time
}

// appController serves the read-only /app dashboard listing the forms and
// files of every tenant.
type appController struct {
	forms forms.Repository
	files files.Repository
}

// Index renders the dashboard.
func (c *appController) Index(w http.ResponseWriter, r *http.Request) {
	page, err := c.load(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	// Render to a buffer first so a template error still yields a clean 500.
	var buf bytes.Buffer
	if err := appTemplate.Execute(&buf, page); err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", appContentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	buf.WriteTo(w)
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
	return page, nil
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
