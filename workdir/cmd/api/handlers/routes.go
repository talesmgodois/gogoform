// Package handlers holds the HTTP controllers of the API and the routes
// mounting them.
package handlers

import (
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	// Registers the generated spec served under /swagger/.
	_ "app/docs"
	"app/internal/config"
	"app/internal/db"
	"app/internal/handler"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
)

// Services holds the domain repositories the controllers are built on.
type Services struct {
	tenants     tenants.Repository
	forms       forms.Repository
	submissions submissions.Repository
	webhooks    webhooks.Repository
	files       files.Repository
}

// NewServices returns the sqlc-backed implementation of every repository.
func NewServices(q db.Querier) Services {
	return Services{
		tenants:     tenants.NewService(q),
		forms:       forms.NewService(q),
		submissions: submissions.NewService(q),
		webhooks:    webhooks.NewService(q),
		files:       files.NewService(q),
	}
}

// Routes builds the HTTP handler. The /app dashboard is only mounted when
// appCfg holds credentials, because it lists every tenant's data.
func Routes(svc Services, appCfg config.AppConfig) http.Handler {
	tenantsCtl := &tenantsController{tenants: svc.tenants}
	formsCtl := &formsController{forms: svc.forms}
	submissionsCtl := &submissionsController{forms: svc.forms, submissions: svc.submissions}
	webhooksCtl := &webhooksController{forms: svc.forms, webhooks: svc.webhooks}
	filesCtl := &filesController{files: svc.files}
	auth := tenantsCtl.Authenticate

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("POST /tenants", tenantsCtl.Create)
	mux.HandleFunc("GET /tenants/me", auth(tenantsCtl.Me))

	mux.HandleFunc("POST /forms", auth(formsCtl.Create))
	mux.HandleFunc("GET /forms", auth(formsCtl.List))
	mux.HandleFunc("GET /forms/{id}", auth(formsCtl.Get))
	mux.HandleFunc("PUT /forms/{id}", auth(formsCtl.Update))
	mux.HandleFunc("DELETE /forms/{id}", auth(formsCtl.Delete))

	mux.HandleFunc("GET /forms/{id}/submissions", auth(submissionsCtl.List))

	mux.HandleFunc("POST /forms/{id}/webhooks", auth(webhooksCtl.Create))
	mux.HandleFunc("GET /forms/{id}/webhooks", auth(webhooksCtl.List))

	mux.HandleFunc("POST /files", auth(filesCtl.Create))
	mux.HandleFunc("GET /files/{id}", filesCtl.Get)
	mux.HandleFunc("DELETE /files/{id}", auth(filesCtl.Delete))

	mux.HandleFunc("GET /public/forms/{slug}", formsCtl.GetPublic)
	mux.HandleFunc("POST /public/forms/{slug}/submissions", submissionsCtl.Create)

	if appCfg.Enabled() {
		appCtl := &appController{forms: svc.forms, files: svc.files}
		mux.HandleFunc("GET /app", basicAuth(appCfg, appCtl.Index))
		mux.Handle("GET /app/{$}", http.RedirectHandler("/app", http.StatusMovedPermanently))
	}
	return mux
}
