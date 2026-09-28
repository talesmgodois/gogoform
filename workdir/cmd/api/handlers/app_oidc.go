package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/auth/oidc"
)

// oidcFlowCookie carries the secrets of an OIDC sign-in in progress from
// /app/oidc/login to /app/oidc/callback. It is HMAC-signed, so the browser
// can hold it but not forge or alter it.
const oidcFlowCookie = "gogoform_oidc_flow"

// oidcFlowPath scopes oidcFlowCookie to the OIDC routes.
const oidcFlowPath = "/app/oidc"

// oidcFlowTTL is how long a sign-in may take at the provider.
const oidcFlowTTL = 10 * time.Minute

// OIDCClient runs the authorization code flow against the provider;
// *oidc.Client implements it.
type OIDCClient interface {
	AuthCodeURL(f oidc.Flow) string
	Exchange(ctx context.Context, code string, f oidc.Flow) (auth.ExternalIdentity, error)
}

// OIDCOptions enables sign-in through an external OpenID Connect provider on
// the /app pages; see Services.WithOIDC.
type OIDCOptions struct {
	Client OIDCClient
	// ProviderName labels the "Sign in with <name>" button.
	ProviderName string
	// FlowKey signs the flow cookie. Use a secret shared by every replica,
	// such as the JWT secret, so the callback may reach any of them.
	FlowKey []byte
	// Now returns the current time; time.Now when nil.
	Now func() time.Time
}

// appOIDC is the OIDC sign-in of the /app pages.
type appOIDC struct {
	client       OIDCClient
	providerName string
	// key is derived from OIDCOptions.FlowKey, so the flow cookie signature
	// can't be confused with anything else signed with that secret.
	key []byte
	now func() time.Time
}

func newAppOIDC(o OIDCOptions) *appOIDC {
	mac := hmac.New(sha256.New, o.FlowKey)
	mac.Write([]byte("gogoform oidc flow cookie"))
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &appOIDC{client: o.Client, providerName: o.ProviderName, key: mac.Sum(nil), now: now}
}

// oidcFlowState is the content of oidcFlowCookie.
type oidcFlowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	// Next is the local page to open once signed in, already safeNext'd.
	Next    string `json:"next"`
	Expires int64  `json:"exp"`
}

func (s oidcFlowState) flow() oidc.Flow {
	return oidc.Flow{State: s.State, Nonce: s.Nonce, Verifier: s.Verifier}
}

// seal encodes s as "<payload>.<signature>", both base64url.
func (o *appOIDC) seal(s oidcFlowState) (string, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + base64.RawURLEncoding.EncodeToString(o.sign(p)), nil
}

// open verifies the signature and expiry of a sealed cookie value.
func (o *appOIDC) open(value string) (oidcFlowState, error) {
	p, sig, ok := strings.Cut(value, ".")
	if !ok {
		return oidcFlowState{}, errors.New("malformed flow cookie")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, o.sign(p)) {
		return oidcFlowState{}, errors.New("invalid flow cookie signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return oidcFlowState{}, errors.New("malformed flow cookie")
	}
	var s oidcFlowState
	if err := json.Unmarshal(payload, &s); err != nil {
		return oidcFlowState{}, errors.New("malformed flow cookie")
	}
	if o.now().Unix() > s.Expires {
		return oidcFlowState{}, errors.New("expired flow cookie")
	}
	return s, nil
}

func (o *appOIDC) sign(payload string) []byte {
	mac := hmac.New(sha256.New, o.key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// loginURL is the link of the "Sign in with <provider>" button.
func (o *appOIDC) loginURL(next string) string {
	return oidcFlowPath + "/login?next=" + url.QueryEscape(next)
}

// OIDCLogin starts a sign-in at the OIDC provider.
//
//	@Summary		Start OIDC sign-in (browser)
//	@Description	Only registered when OIDC is configured. Starts the authorization code flow with PKCE: stores the flow in a short-lived signed cookie and redirects the browser to the provider. Meant for the /app pages, not for API clients.
//	@Tags			app
//	@Param			next	query	string	false	"Local /app page to open once signed in"
//	@Success		302		"Redirect to the provider"
//	@Router			/app/oidc/login [get]
func (c *appController) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	f, err := oidc.NewFlow()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	value, err := c.oidc.seal(oidcFlowState{
		State:    f.State,
		Nonce:    f.Nonce,
		Verifier: f.Verifier,
		Next:     safeNext(r.URL.Query().Get("next")),
		Expires:  c.oidc.now().Add(oidcFlowTTL).Unix(),
	})
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcFlowCookie,
		Value:    value,
		Path:     oidcFlowPath,
		MaxAge:   int(oidcFlowTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		// Lax: the cookie must come back on the provider's top-level
		// redirect to the callback.
		SameSite: http.SameSiteLaxMode,
	})
	setAppNoStoreHeaders(w.Header())
	http.Redirect(w, r, c.oidc.client.AuthCodeURL(f), http.StatusFound)
}

// OIDCCallback completes a sign-in at the OIDC provider: it checks the flow,
// exchanges the code, verifies the ID token and signs the user in exactly
// like the password sign-in does.
//
//	@Summary		Complete OIDC sign-in (browser)
//	@Description	Only registered when OIDC is configured. The provider redirects here: the state is checked against the flow cookie, the code is exchanged with the PKCE verifier and the ID token verified (signature, issuer, audience, expiry, nonce). The user is then found or created, the session cookie set and the browser redirected to the page it came from. Errors render an HTML page.
//	@Tags			app
//	@Param			code				query	string	false	"Authorization code"
//	@Param			state				query	string	false	"State of the flow"
//	@Param			error				query	string	false	"Error reported by the provider"
//	@Param			error_description	query	string	false	"Description of the provider error"
//	@Success		303					"Signed in; redirect to the next page"
//	@Failure		400					"Invalid, expired or cancelled sign-in"
//	@Failure		401					"The ID token could not be verified"
//	@Failure		403					"The account is not allowed to sign in"
//	@Router			/app/oidc/callback [get]
func (c *appController) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	// The flow is single-use whatever happens next.
	clearOIDCFlowCookie(w, r)
	setAppNoStoreHeaders(w.Header())

	if providerErr := q.Get("error"); providerErr != "" {
		slog.WarnContext(ctx, "oidc: provider returned an error", "error", providerErr, "description", q.Get("error_description"))
		renderAppMessage(w, r, http.StatusBadRequest, "Sign-in cancelled",
			"The sign-in with "+c.oidc.providerName+" did not complete. You can try again.")
		return
	}

	var flow oidcFlowState
	cookie, err := r.Cookie(oidcFlowCookie)
	if err == nil {
		flow, err = c.oidc.open(cookie.Value)
	}
	if err != nil {
		slog.WarnContext(ctx, "oidc: rejected callback", "reason", "missing, invalid or expired flow cookie")
		renderAppMessage(w, r, http.StatusBadRequest, "Sign-in expired",
			"This sign-in attempt expired or was started in another browser. Please try again.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(flow.State)) != 1 {
		slog.WarnContext(ctx, "oidc: rejected callback", "reason", "state mismatch")
		renderAppMessage(w, r, http.StatusBadRequest, "Sign-in failed",
			"This sign-in attempt does not match the one started in this browser. Please try again.")
		return
	}
	code := q.Get("code")
	if code == "" {
		slog.WarnContext(ctx, "oidc: rejected callback", "reason", "missing code")
		renderAppMessage(w, r, http.StatusBadRequest, "Sign-in failed", "The identity provider did not return an authorization code.")
		return
	}

	id, err := c.oidc.client.Exchange(ctx, code, flow.flow())
	if err != nil {
		slog.WarnContext(ctx, "oidc: rejected callback", "reason", err.Error())
		if errors.Is(err, oidc.ErrEmailNotAllowed) {
			renderAppMessage(w, r, http.StatusForbidden, "Account not allowed",
				"Your "+c.oidc.providerName+" account needs a verified email address in an allowed domain to sign in here.")
			return
		}
		renderAppMessage(w, r, http.StatusUnauthorized, "Sign-in failed",
			"Your identity could not be verified with "+c.oidc.providerName+". Please try again.")
		return
	}

	tok, err := c.auth.SignInExternal(ctx, id)
	if err != nil {
		appErr, ok := apperrors.As(err)
		if ok && (appErr.Code == apperrors.CodeForbidden || appErr.Code == apperrors.CodeUnauthorized) {
			slog.WarnContext(ctx, "oidc: sign-in refused", "issuer", id.Issuer, "subject", id.Subject, "reason", appErr.Message)
			renderAppMessage(w, r, http.StatusForbidden, "Sign-in refused", appErr.Message+".")
			return
		}
		slog.ErrorContext(ctx, "oidc: sign-in failed", "issuer", id.Issuer, "subject", id.Subject, "error", err)
		renderAppMessage(w, r, http.StatusInternalServerError, "Sign-in failed", "Something went wrong while signing you in. Please try again.")
		return
	}

	slog.InfoContext(ctx, "oidc: signed in", "issuer", id.Issuer, "subject", id.Subject, "user_id", tok.User.ID)
	setSessionCookie(w, r, tok)
	http.Redirect(w, r, flow.Next, http.StatusSeeOther)
}

func clearOIDCFlowCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcFlowCookie, Value: "", Path: oidcFlowPath, MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}
