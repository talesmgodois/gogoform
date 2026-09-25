package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
)

// sessionCookie holds the JWT of the user signed in to the /app pages. It is
// HttpOnly and scoped to /app, and only guardPage reads it: the API never
// accepts it, so it cannot be used for cross-site requests against the API.
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
	Routes []appRouteAccess
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
}

// sessionUser returns the user of the session cookie, or nil when there is
// none. An invalid or expired cookie is cleared.
func (c *appController) sessionUser(w http.ResponseWriter, r *http.Request) *auth.User {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil
	}
	u, err := c.auth.CheckToken(r.Context(), cookie.Value)
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
		Path:     "/app",
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

// Account renders the signed-in user and the access of every /app route:
// a page to try the route policies out.
func (c *appController) Account(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	page := appAccountPage{appLayout: appLayout{Active: "account"}}
	for _, rt := range c.mounted {
		page.Routes = append(page.Routes, appRouteAccess{Pattern: rt.Pattern, Access: describeAccess(rt), Allowed: rt.Access.Allows(u)})
	}
	renderAppPage(w, r, appAccountTemplate, &page)
}

// Users lists the user accounts.
func (c *appController) Users(w http.ResponseWriter, r *http.Request) {
	users, err := c.auth.ListUsers(r.Context(), auth.Page{Limit: appListLimit})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	renderAppPage(w, r, appUsersTemplate, &appUsersPage{appLayout: appLayout{Active: "users"}, Users: users})
}

// renderAppMessage renders a page showing a message, with the given status.
func renderAppMessage(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	renderAppPageStatus(w, r, status, appMessageTemplate, &appMessagePage{Title: title, Message: message})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/app", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

// safeNext returns next if it is a local /app page, so the sign-in page
// cannot be used as an open redirect, and defaultSignInRedirect otherwise.
func safeNext(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || strings.Contains(next, `\`) ||
		!(u.Path == "/app" || strings.HasPrefix(u.Path, "/app/")) || strings.HasPrefix(u.Path, "/app/signin") {
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
