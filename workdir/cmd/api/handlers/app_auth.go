package handlers

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/tenants"
)

// sessionCookie holds the JWT of the user signed in to the /app pages. It is
// HttpOnly, SameSite=Lax and scoped to "/" so the pages' scripts can call the
// tenant API with it. guardPage reads it for the /app pages and tenantOrUser
// for the tenant API, which rejects cross-origin state-changing requests
// authenticated by it. The public form page and endpoints read it as an
// optional user (see PublicForm); the Guard routes (/auth, /users) never
// accept it.
const sessionCookie = "gogoform_session"

// defaultSignInRedirect is where the sign-in page leads without a next page.
const defaultSignInRedirect = "/app/account"

// appSignInPage is the data rendered by templates/signin.html.
type appSignInPage struct {
	appLayout
	// Next is the local page to open once signed in.
	Next string
}

// appAccountPage is the data rendered by templates/account.html.
type appAccountPage struct {
	appLayout
	// Workspace is the viewer's tenant; nil when they have none.
	Workspace *tenants.Tenant
	Routes    []appRouteAccess
}

// appRouteAccess describes an /app route and whether the viewer may open it.
type appRouteAccess struct {
	Pattern, Access string
	Allowed         bool
}

// appUsersPage is the data rendered by templates/users.html.
type appUsersPage struct {
	appLayout
	Users []auth.User
}

// appMessagePage is the data rendered by templates/message.html.
type appMessagePage struct {
	appLayout
	Title, Message string
	// Href and Link are where the page sends the viewer next; the account
	// page when empty.
	Href, Link string
}

// sessionUser returns the user of the session cookie, or nil when there is
// none. An invalid or expired cookie is cleared.
func (c *appController) sessionUser(w http.ResponseWriter, r *http.Request) *auth.User {
	return sessionUserOf(w, r, c.auth)
}

// sessionUserOf returns the user of the session cookie, checked by svc, or
// nil when there is none. An invalid or expired cookie is cleared.
func sessionUserOf(w http.ResponseWriter, r *http.Request, svc *auth.Service) *auth.User {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil
	}
	u, err := svc.CheckToken(r.Context(), cookie.Value)
	if err != nil {
		clearSessionCookie(w, r)
		return nil
	}
	return &u
}

// SignInPage renders the sign-in and sign-up form.
func (c *appController) SignInPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if userFrom(r.Context()) != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	renderAppPage(w, r, appSignInTemplate, &appSignInPage{appLayout: appLayout{Active: "signin"}, Next: next})
}

// SignIn checks the HTTP Basic credentials sent by the sign-in page and
// stores the issued token in the session cookie.
func (c *appController) SignIn(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apperrors.WriteHTTP(w, r, apperrors.NewForbidden("cross-origin request"))
		return
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		apperrors.WriteHTTP(w, r, apperrors.NewUnauthorized("missing Basic credentials"))
		return
	}
	tok, err := c.auth.SignIn(r.Context(), username, password)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok.AccessToken,
		Path:     "/",
		Expires:  tok.ExpiresAt,
		MaxAge:   int(time.Until(tok.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	setAppNoStoreHeaders(w.Header())
	writeJSON(w, r, http.StatusOK, toUserResponse(tok.User))
}

// SignOut clears the session cookie.
func (c *appController) SignOut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apperrors.WriteHTTP(w, r, apperrors.NewForbidden("cross-origin request"))
		return
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Account renders the signed-in user, their workspace and the access of
// every /app route: a page to try the route policies out.
func (c *appController) Account(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	page := appAccountPage{appLayout: appLayout{Active: "account"}}
	t, err := c.tenants.GetByOwner(r.Context(), u.ID)
	switch {
	case err == nil:
		page.Workspace = &t
	case !isNotFound(err):
		renderAppError(w, r, err)
		return
	}
	for _, rt := range c.mounted {
		page.Routes = append(page.Routes, appRouteAccess{Pattern: rt.Pattern, Access: describeAccess(rt), Allowed: rt.Access.Allows(u)})
	}
	renderAppPage(w, r, appAccountTemplate, &page)
}

// Users lists the user accounts.
func (c *appController) Users(w http.ResponseWriter, r *http.Request) {
	users, err := c.auth.ListUsers(r.Context(), auth.Page{Limit: appListLimit})
	if err != nil {
		renderAppError(w, r, err)
		return
	}
	renderAppPage(w, r, appUsersTemplate, &appUsersPage{appLayout: appLayout{Active: "users"}, Users: users})
}

// renderAppMessage renders a page showing a message, with the given status.
func renderAppMessage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	renderAppPageStatus(w, r, status, appMessageTemplate, &appMessagePage{Title: title, Message: message})
}

// renderAppError renders err as a message page, the HTML counterpart of
// apperrors.WriteHTTP for the /app pages: malformed and unknown form IDs get
// a 400 and a 404 page linking back to the dashboard, features missing from
// the workspace's plan a 403 page linking to the billing page, and 5xx
// errors are logged and shown without their details.
func renderAppError(w http.ResponseWriter, r *http.Request, err error) {
	status := apperrors.HTTPStatus(err)
	page := appMessagePage{Href: "/app", Link: "Back to your forms"}
	appErr, _ := apperrors.As(err)
	switch {
	case status == http.StatusBadRequest:
		page.Title, page.Message = "Invalid form", "The form address is malformed. Open the form from your dashboard."
	case status == http.StatusNotFound:
		page.Title, page.Message = "Form not found", "This form does not exist or is not in your workspace."
	case appErr != nil && appErr.Code == apperrors.CodePlanFeatureUnavailable:
		page.Title, page.Message = "Upgrade to Pro", appErr.Message
		page.Href, page.Link = "/app/billing", "See plans"
	case status >= http.StatusInternalServerError:
		slog.ErrorContext(r.Context(), "request failed", "status", status, "error", err)
		page.Title, page.Message = "Something went wrong", "We could not load this page. Try again in a moment."
	default:
		page.Title, page.Message = http.StatusText(status), "This request could not be completed."
		if appErr != nil {
			page.Message = appErr.Message
		}
	}
	renderAppPageStatus(w, r, status, appMessageTemplate, &page)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

// safeNext returns next if it is a local /app page or a public form page
// (/f/...), so the sign-in page cannot be used as an open redirect, and
// defaultSignInRedirect otherwise.
func safeNext(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || strings.Contains(next, `\`) ||
		!(u.Path == "/app" || strings.HasPrefix(u.Path, "/app/") || strings.HasPrefix(u.Path, "/f/")) || strings.HasPrefix(u.Path, "/app/signin") {
		return defaultSignInRedirect
	}
	return u.RequestURI()
}

// sameOrigin reports whether a state-changing request comes from a page of
// this server. Browsers send Origin on every cross-origin POST.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// isHTTPS reports whether the client reached the server over HTTPS, directly
// or through a TLS-terminating proxy, to mark cookies Secure.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
