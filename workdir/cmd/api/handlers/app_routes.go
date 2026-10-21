package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"app/internal/pkg/auth"
)

// appRoute is a route of the /app frontend. Who may open it is declared here,
// next to its pattern, rather than inside the handler: set Access to
// auth.Public, auth.SignedIn() (any role) or auth.SignedIn(roles...).
type appRoute struct {
	Pattern string
	Handler http.HandlerFunc
	// Access is checked against the user of the session cookie. Anonymous
	// visitors of a non-public page are sent to the sign-in page; users
	// whose role is not allowed get a 403 page.
	Access auth.Policy
	// Workspace routes show the data of the viewer's own tenant, which
	// guardPage resolves and stores for tenantFrom. Viewers without a tenant
	// get a 403 page.
	Workspace bool
	// Nav, when set, lists the route in the navigation bar for the users it
	// allows.
	Nav *navTab
}

// routes lists every /app route.
func (c *appController) routes() []appRoute {
	return []appRoute{
		{Pattern: "GET /app", Handler: c.Index, Access: auth.SignedIn(), Workspace: true, Nav: &navTab{Key: "dashboard", Label: "Forms"}},
		{Pattern: "GET /app/forms/{id}", Handler: c.Form, Access: auth.SignedIn(), Workspace: true},
		{Pattern: "GET /app/forms/{id}/submissions.json", Handler: c.ExportJSON, Access: auth.SignedIn(), Workspace: true},
		{Pattern: "GET /app/forms/{id}/submissions.csv", Handler: c.ExportCSV, Access: auth.SignedIn(), Workspace: true},
		{Pattern: "GET /app/builder", Handler: c.Builder, Access: auth.SignedIn(), Workspace: true},

		{Pattern: "GET /app/signin", Handler: c.SignInPage, Access: auth.Public},
		{Pattern: "POST /app/signin", Handler: c.limiter.limit(rateLimitSignin, c.SignIn), Access: auth.Public},
		{Pattern: "POST /app/signout", Handler: c.SignOut, Access: auth.Public},
		{Pattern: "GET /app/verify", Handler: c.Verify, Access: auth.Public},
		{Pattern: "POST /app/verify/resend", Handler: c.ResendVerificationEmail, Access: auth.SignedIn()},
		{Pattern: "GET /app/forgot-password", Handler: c.ForgotPasswordPage, Access: auth.Public},
		{Pattern: "POST /app/forgot-password", Handler: c.limiter.limit(rateLimitForgotPassword, c.ForgotPassword), Access: auth.Public},
		{Pattern: "GET /app/reset-password", Handler: c.ResetPasswordPage, Access: auth.Public},
		{Pattern: "POST /app/reset-password", Handler: c.ResetPassword, Access: auth.Public},
		{Pattern: "GET /app/account", Handler: c.Account, Access: auth.SignedIn(), Nav: &navTab{Key: "account", Label: "Account"}},
		{Pattern: "POST /app/account/email", Handler: c.limiter.limit(rateLimitSignin, c.ChangeEmail), Access: auth.SignedIn()},
		{Pattern: "POST /app/account/password", Handler: c.limiter.limit(rateLimitSignin, c.ChangePassword), Access: auth.SignedIn()},
		{Pattern: "POST /app/account/delete", Handler: c.DeleteAccount, Access: auth.SignedIn()},
		{Pattern: "GET /app/billing", Handler: c.Billing, Access: auth.SignedIn(), Workspace: true, Nav: &navTab{Key: "billing", Label: "Billing"}},
		{Pattern: "GET /app/admin", Handler: c.AdminIndex, Access: auth.SignedIn(auth.RoleAdmin), Nav: &navTab{Key: "admin", Label: "All tenants"}},
		{Pattern: "GET /app/admin/forms/{id}", Handler: c.AdminForm, Access: auth.SignedIn(auth.RoleAdmin)},
		{Pattern: "GET /app/admin/forms/{id}/submissions.json", Handler: c.AdminExportJSON, Access: auth.SignedIn(auth.RoleAdmin)},
		{Pattern: "GET /app/admin/forms/{id}/submissions.csv", Handler: c.AdminExportCSV, Access: auth.SignedIn(auth.RoleAdmin)},
		{Pattern: "GET /app/users", Handler: c.Users, Access: auth.SignedIn(auth.RoleAdmin), Nav: &navTab{Key: "users", Label: "Users"}},
	}
}

// mount registers the routes on mux.
func (c *appController) mount(mux *http.ServeMux) {
	c.mounted = nil
	for _, rt := range c.routes() {
		mux.HandleFunc(rt.Pattern, c.guardPage(rt))
		if rt.Nav != nil {
			rt.Nav.Href = routePath(rt.Pattern)
		}
		c.mounted = append(c.mounted, rt)
	}
	mux.Handle("GET /app/{$}", http.RedirectHandler("/app", http.StatusMovedPermanently))
}

// guardPage wraps the handler of rt with its Access check and, for workspace
// routes, resolves the viewer's tenant. The session user and the navigation
// tabs they may see are then available to renderAppPage.
func (c *appController) guardPage(rt appRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := c.sessionUser(w, r)
		if !rt.Access.Allows(u) {
			if u == nil {
				http.Redirect(w, r, "/app/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			ctx := withAppView(withUser(r.Context(), u), c.tabsFor(u))
			renderAppMessage(w, r.WithContext(ctx), http.StatusForbidden, "Access denied",
				"Your role ("+string(u.Role)+") does not grant access to this page.")
			return
		}
		ctx := withAppView(withUser(r.Context(), u), c.tabsFor(u))
		if rt.Workspace {
			t, err := c.tenants.GetByOwner(ctx, u.ID)
			if isNotFound(err) {
				renderAppMessage(w, r.WithContext(ctx), http.StatusForbidden, "No workspace",
					"Your account has no workspace, so there are no forms or files to show.")
				return
			}
			if err != nil {
				renderAppError(w, r.WithContext(ctx), err)
				return
			}
			ctx = withTenant(ctx, t)
		}
		rt.Handler(w, r.WithContext(ctx))
	}
}

// tabsFor returns the navigation tabs of the mounted routes u may open.
func (c *appController) tabsFor(u *auth.User) []navTab {
	var tabs []navTab
	for _, rt := range c.mounted {
		if rt.Nav != nil && rt.Access.Allows(u) {
			tabs = append(tabs, *rt.Nav)
		}
	}
	return tabs
}

// routePath returns the path of a "METHOD /path" pattern.
func routePath(pattern string) string {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}

// appViewKey is the context key of the tabs set by guardPage.
type appViewKey struct{}

func withAppView(ctx context.Context, tabs []navTab) context.Context {
	return context.WithValue(ctx, appViewKey{}, tabs)
}

func tabsFrom(ctx context.Context) []navTab {
	tabs, _ := ctx.Value(appViewKey{}).([]navTab)
	return tabs
}
