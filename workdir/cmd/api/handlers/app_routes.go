package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"app/internal/config"
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
	// Operator routes show every tenant's data, so they also require the
	// HTTP Basic credentials of config.AppConfig and are only mounted when
	// those are set.
	Operator bool
	// Nav, when set, lists the route in the navigation bar for the users it
	// allows.
	Nav *navTab
}

// routes lists every /app route.
func (c *appController) routes() []appRoute {
	return []appRoute{
		// The dashboard and the builder are not tied to user accounts yet:
		// they keep using the operator credentials only.
		{Pattern: "GET /app", Handler: c.Index, Access: auth.Public, Operator: true, Nav: &navTab{Key: "dashboard", Label: "Dashboard"}},
		{Pattern: "GET /app/forms/{id}", Handler: c.Form, Access: auth.Public, Operator: true},
		{Pattern: "GET /app/forms/{id}/submissions.json", Handler: c.ExportJSON, Access: auth.Public, Operator: true},
		{Pattern: "GET /app/forms/{id}/submissions.csv", Handler: c.ExportCSV, Access: auth.Public, Operator: true},
		{Pattern: "GET /app/builder", Handler: c.Builder, Access: auth.Public, Operator: true, Nav: &navTab{Key: "builder", Label: "Form builder"}},

		{Pattern: "GET /app/signin", Handler: c.SignInPage, Access: auth.Public},
		{Pattern: "POST /app/signin", Handler: c.SignIn, Access: auth.Public},
		{Pattern: "POST /app/signout", Handler: c.SignOut, Access: auth.Public},
		{Pattern: "GET /app/account", Handler: c.Account, Access: auth.SignedIn(), Nav: &navTab{Key: "account", Label: "Account"}},
		{Pattern: "GET /app/users", Handler: c.Users, Access: auth.SignedIn(auth.RoleAdmin), Nav: &navTab{Key: "users", Label: "Users"}},
	}
}

// mount registers the routes on mux; the operator routes only when cfg holds
// credentials.
func (c *appController) mount(mux *http.ServeMux, cfg config.AppConfig) {
	c.mounted = nil
	for _, rt := range c.routes() {
		if rt.Operator && !cfg.Enabled() {
			continue
		}
		h := c.guardPage(rt)
		if rt.Operator {
			h = basicAuth(cfg, h)
		}
		mux.HandleFunc(rt.Pattern, h)
		if rt.Nav != nil {
			rt.Nav.Href = routePath(rt.Pattern)
		}
		c.mounted = append(c.mounted, rt)
	}
	if cfg.Enabled() {
		mux.Handle("GET /app/{$}", http.RedirectHandler("/app", http.StatusMovedPermanently))
	}
}

// guardPage wraps the handler of rt with its Access check. The session user
// and the navigation tabs they may see are then available to renderAppPage.
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

// describeAccess renders who may open rt, for the account page.
func describeAccess(rt appRoute) string {
	var s string
	switch roles := rt.Access.Roles(); {
	case rt.Access.IsPublic():
		s = "Public"
	case len(roles) == 0:
		s = "Signed in"
	default:
		names := make([]string, len(roles))
		for i, r := range roles {
			names[i] = string(r)
		}
		s = "Roles: " + strings.Join(names, ", ")
	}
	if rt.Operator {
		s += " + operator credentials"
	}
	return s
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
