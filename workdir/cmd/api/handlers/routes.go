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
	authpkg "app/internal/pkg/auth"
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
	auth        *authpkg.Service
}

// NewServices returns the sqlc-backed implementation of every repository,
// along with authSvc, which authenticates users.
func NewServices(q db.Querier, authSvc *authpkg.Service) Services {
	return Services{
		auth:        authSvc,
		tenants:     tenants.NewService(q),
		forms:       forms.NewService(q),
		submissions: submissions.NewService(q),
		webhooks:    webhooks.NewService(q),
		files:       files.NewService(q),
	}
}

// Routes builds the HTTP handler. Tenant-scoped endpoints authenticate with
// the tenant's API key (auth), user endpoints with a JWT or HTTP Basic
// credentials guarded by an auth.Policy (guard). The /app pages are listed in
// app_routes.go; the operator ones, which list every tenant's data, are only
// mounted when appCfg holds credentials.
func Routes(svc Services, appCfg config.AppConfig) http.Handler {
	tenantsCtl := &tenantsController{tenants: svc.tenants}
	formsCtl := &formsController{forms: svc.forms}
	submissionsCtl := &submissionsController{forms: svc.forms, submissions: svc.submissions}
	webhooksCtl := &webhooksController{forms: svc.forms, webhooks: svc.webhooks}
	filesCtl := &filesController{files: svc.files}
	authCtl := &authController{auth: svc.auth}
	auth := tenantsCtl.Authenticate
	guard := authCtl.Guard

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("POST /auth/signup", authCtl.SignUp)
	mux.HandleFunc("POST /auth/signin", authCtl.SignIn)
	mux.HandleFunc("GET /auth/me", guard(authpkg.SignedIn(), authCtl.Me))
	mux.HandleFunc("GET /users", guard(authpkg.SignedIn(authpkg.RoleAdmin), authCtl.ListUsers))
	mux.HandleFunc("PUT /users/{id}/role", guard(authpkg.SignedIn(authpkg.RoleAdmin), authCtl.SetRole))

	// Examples of every kind of policy.
	mux.HandleFunc("GET /samples/public", guard(authpkg.Public, SamplePublic))
	mux.HandleFunc("GET /samples/signed-in", guard(authpkg.SignedIn(), SampleSignedIn))
	mux.HandleFunc("GET /samples/form-creator", guard(authpkg.SignedIn(authpkg.RoleFormCreator), SampleFormCreator))
	mux.HandleFunc("GET /samples/admin", guard(authpkg.SignedIn(authpkg.RoleAdmin), SampleAdmin))

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

	// Credentials are optional: private forms check the user themselves.
	mux.HandleFunc("GET /public/forms/{slug}", guard(authpkg.Public, formsCtl.GetPublic))
	mux.HandleFunc("POST /public/forms/{slug}/submissions", guard(authpkg.Public, submissionsCtl.Create))

	appCtl := &appController{forms: svc.forms, files: svc.files, submissions: svc.submissions, auth: svc.auth}
	appCtl.mount(mux, appCfg)
	return mux
}
