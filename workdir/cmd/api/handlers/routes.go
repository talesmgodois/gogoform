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
	"app/internal/pkg/billing"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/mailer"
	"app/internal/pkg/plans"
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
	// billing is nil when Stripe is not configured.
	billing *billing.Service
	// plans enforces plan limits, counting usage through the repositories.
	plans *plans.Enforcer
	// mail sends the new-submission notification; typically mailer.Async so
	// it never blocks a request.
	mail mailer.Mailer
	// turnstile overrides the Cloudflare Turnstile verifier built from the
	// security config; tests set a fake. nil uses the real one.
	turnstile TurnstileVerifier
}

// NewServices returns the sqlc-backed implementation of every repository and
// the plan Enforcer over them, along with authSvc, which authenticates users, billingSvc, which is nil
// when Stripe is not configured, and mail, which sends the new-submission notification.
func NewServices(q db.Querier, authSvc *authpkg.Service, billingSvc *billing.Service, mail mailer.Mailer) Services {
	formsSvc, submissionsSvc, filesSvc := forms.NewService(q), submissions.NewService(q), files.NewService(q)
	return Services{
		auth:        authSvc,
		billing:     billingSvc,
		tenants:     tenants.NewService(q),
		forms:       formsSvc,
		submissions: submissionsSvc,
		webhooks:    webhooks.NewService(q),
		files:       filesSvc,
		plans:       plans.NewEnforcer(formsSvc, submissionsSvc, filesSvc),
		mail:        mail,
	}
}

// Routes builds the HTTP handler. Tenant-scoped endpoints authenticate with
// the tenant's API key or resolve the own tenant of a user signed in with a
// JWT, HTTP Basic credentials or the /app session cookie (auth; see
// tenantOrUser). Respondents fill public forms in on the /f/{slug} page
// (public_page.go). User endpoints accept a JWT or HTTP Basic credentials, never
// the session cookie, guarded by an auth.Policy (guard). The /app pages,
// listed in app_routes.go, use the session cookie of a signed-in user. The
// development-only POST /tenants and /samples/* are only mounted when enabled
// in features. The billing routes (billing.go) are only mounted when stripe is
// enabled and svc has a billing service, so they 404 otherwise; their return
// URLs are built from app.BaseURL. security configures the rate limits of the
// sign-in, sign-up, forgot-password and public submission/upload routes, the
// reverse proxies trusted to supply the respondent's real IP, and the spam
// checks of public submissions (minimum completion time, Turnstile).
func Routes(svc Services, features config.FeaturesConfig, stripe config.StripeConfig, app config.AppConfig, security config.SecurityConfig) http.Handler {
	rl := newRateLimiters(security)
	turnstile := svc.turnstile
	if turnstile == nil && security.TurnstileSecret != "" {
		turnstile = newCloudflareTurnstile(security.TurnstileSecret)
	}
	turnstileSiteKey := ""
	if turnstile != nil {
		turnstileSiteKey = security.TurnstileSiteKey
	}

	tenantsCtl := &tenantsController{tenants: svc.tenants}
	formsCtl := &formsController{forms: svc.forms, plans: svc.plans, auth: svc.auth}
	submissionsCtl := &submissionsController{
		forms: svc.forms, submissions: svc.submissions, files: svc.files, plans: svc.plans,
		tenants: svc.tenants, auth: svc.auth, mail: svc.mail, baseURL: app.BaseURL, ip: rl.ip,
		spam: spamGuard{minSeconds: int32(security.MinFormSeconds), turnstile: turnstile, ip: rl.ip},
	}
	webhooksCtl := &webhooksController{forms: svc.forms, webhooks: svc.webhooks}
	filesCtl := &filesController{files: svc.files, forms: svc.forms, plans: svc.plans}
	authCtl := &authController{auth: svc.auth}
	auth := tenantOrUser(svc.tenants, authCtl)
	guard := authCtl.Guard

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)
	mux.HandleFunc("GET /static/form-render.js", FormRenderScript)

	mux.HandleFunc("POST /auth/signup", rl.limit(rateLimitSignup, authCtl.SignUp))
	mux.HandleFunc("POST /auth/signin", rl.limit(rateLimitSignin, authCtl.SignIn))
	mux.HandleFunc("GET /auth/me", guard(authpkg.SignedIn(), authCtl.Me))
	mux.HandleFunc("GET /users", guard(authpkg.SignedIn(authpkg.RoleAdmin), authCtl.ListUsers))
	mux.HandleFunc("PUT /users/{id}/role", guard(authpkg.SignedIn(authpkg.RoleAdmin), authCtl.SetRole))

	if features.SamplesEnabled() {
		// Examples of every kind of policy.
		mux.HandleFunc("GET /samples/public", guard(authpkg.Public, SamplePublic))
		mux.HandleFunc("GET /samples/signed-in", guard(authpkg.SignedIn(), SampleSignedIn))
		mux.HandleFunc("GET /samples/form-creator", guard(authpkg.SignedIn(authpkg.RoleFormCreator), SampleFormCreator))
		mux.HandleFunc("GET /samples/admin", guard(authpkg.SignedIn(authpkg.RoleAdmin), SampleAdmin))
	}

	if features.PublicTenantSignupEnabled() {
		mux.HandleFunc("POST /tenants", tenantsCtl.Create)
	}
	mux.HandleFunc("GET /tenants/me", auth(tenantsCtl.Me))
	mux.HandleFunc("POST /tenants/me/api-key/rotate", auth(tenantsCtl.RotateAPIKey))

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
	// The session cookie lets the /f page submit private forms.
	mux.HandleFunc("GET /public/forms/{slug}", authCtl.PublicForm(formsCtl.GetPublic))
	mux.HandleFunc("POST /public/forms/{slug}/submissions",
		rl.limit(rateLimitPublicSubmission, authCtl.PublicForm(submissionsCtl.Create)))
	mux.HandleFunc("POST /public/forms/{slug}/files",
		rl.limit(rateLimitPublicUpload, authCtl.PublicForm(filesCtl.PublicCreate)))

	publicPageCtl := &publicPageController{forms: svc.forms, auth: svc.auth, plans: svc.plans, turnstileSiteKey: turnstileSiteKey}
	mux.HandleFunc("GET /f/{slug}", publicPageCtl.Form)
	mux.HandleFunc("GET /f/{slug}/thanks", publicPageCtl.Thanks)

	billingEnabled := stripe.Enabled() && svc.billing != nil
	appCtl := &appController{tenants: svc.tenants, forms: svc.forms, files: svc.files, submissions: svc.submissions, auth: svc.auth,
		plans: svc.plans, billingEnabled: billingEnabled, limiter: rl}
	appCtl.mount(mux)

	if billingEnabled {
		billingCtl := &billingController{billing: svc.billing, stripe: stripe, baseURL: app.BaseURL, app: appCtl}
		billingCtl.mount(mux)
	}
	return mux
}
