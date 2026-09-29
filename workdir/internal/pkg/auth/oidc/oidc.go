// Package oidc signs users in through an external OpenID Connect provider
// with the authorization code flow and PKCE. It only authenticates people:
// the verified identity is handed to auth.Service.SignInExternal, which
// issues the app's own token. Provider tokens are never kept.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"app/internal/pkg/auth"
)

// Config configures a Client.
type Config struct {
	IssuerURL string
	ClientID  string
	// ClientSecret is empty for public clients, which rely on PKCE alone.
	ClientSecret string
	RedirectURL  string
	// Scopes requested; "openid" is added when missing.
	Scopes []string
	// AllowedEmailDomains, when set, only lets in users whose verified
	// email is in one of these (lowercase) domains.
	AllowedEmailDomains []string
	// Now returns the current time for the ID token expiry check;
	// time.Now when nil.
	Now func() time.Time
}

// Errors returned by Client.Exchange, so callers can tell the person why
// they were turned away. Any other error is a failed exchange or an invalid
// ID token.
var (
	// ErrNonceMismatch means the ID token was not issued for this flow.
	ErrNonceMismatch = errors.New("oidc: ID token nonce does not match")
	// ErrEmailNotAllowed means AllowedEmailDomains rejected the user.
	ErrEmailNotAllowed = errors.New("oidc: email is not verified or not in an allowed domain")
)

// Client runs the authorization code flow against one provider.
type Client struct {
	oauth    oauth2.Config
	verifier *gooidc.IDTokenVerifier
	domains  []string
}

// New discovers the provider at cfg.IssuerURL and returns a Client for it.
// It fails when the issuer is unreachable or its discovery document is
// invalid, so a misconfiguration is caught at startup.
func New(ctx context.Context, cfg Config) (*Client, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider at %s: %w", cfg.IssuerURL, err)
	}
	scopes := slices.Clone(cfg.Scopes)
	if !slices.Contains(scopes, gooidc.ScopeOpenID) {
		scopes = append([]string{gooidc.ScopeOpenID}, scopes...)
	}
	return &Client{
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
		verifier: provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID, Now: cfg.Now}),
		domains:  cfg.AllowedEmailDomains,
	}, nil
}

// Flow holds the secrets of one sign-in attempt, kept by the browser in an
// integrity-protected cookie between the redirect to the provider and the
// callback.
type Flow struct {
	// State ties the callback to the browser that started the flow (CSRF).
	State string
	// Nonce ties the ID token to this flow (replay).
	Nonce string
	// Verifier is the PKCE code verifier; only its S256 hash is sent to
	// the provider.
	Verifier string
}

// NewFlow returns a Flow with fresh random secrets.
func NewFlow() (Flow, error) {
	state, err := randomString()
	if err != nil {
		return Flow{}, err
	}
	nonce, err := randomString()
	if err != nil {
		return Flow{}, err
	}
	return Flow{State: state, Nonce: nonce, Verifier: oauth2.GenerateVerifier()}, nil
}

// AuthCodeURL returns the provider URL starting f.
func (c *Client) AuthCodeURL(f Flow) string {
	return c.oauth.AuthCodeURL(f.State, gooidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
}

// idClaims are the ID token claims read besides the standard ones.
type idClaims struct {
	Email             string          `json:"email"`
	EmailVerified     json.RawMessage `json:"email_verified"`
	PreferredUsername string          `json:"preferred_username"`
	Name              string          `json:"name"`
}

// Exchange trades the authorization code of f for tokens, verifies the ID
// token (signature, issuer, audience, expiry and nonce) and returns the
// identity it proves.
func (c *Client) Exchange(ctx context.Context, code string, f Flow) (auth.ExternalIdentity, error) {
	tok, err := c.oauth.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return auth.ExternalIdentity{}, fmt.Errorf("oidc: exchange code: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return auth.ExternalIdentity{}, errors.New("oidc: token response has no id_token")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return auth.ExternalIdentity{}, fmt.Errorf("oidc: verify ID token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(f.Nonce)) != 1 {
		return auth.ExternalIdentity{}, ErrNonceMismatch
	}

	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return auth.ExternalIdentity{}, fmt.Errorf("oidc: read ID token claims: %w", err)
	}
	id := auth.ExternalIdentity{
		Issuer:            idToken.Issuer,
		Subject:           idToken.Subject,
		Email:             strings.TrimSpace(claims.Email),
		EmailVerified:     parseVerified(claims.EmailVerified),
		PreferredUsername: claims.PreferredUsername,
		Name:              claims.Name,
	}
	if !c.emailAllowed(id) {
		return auth.ExternalIdentity{}, ErrEmailNotAllowed
	}
	return id, nil
}

// emailAllowed applies AllowedEmailDomains: with none configured everyone
// is allowed, otherwise the email must be verified and its domain (after
// the last '@') listed.
func (c *Client) emailAllowed(id auth.ExternalIdentity) bool {
	if len(c.domains) == 0 {
		return true
	}
	at := strings.LastIndex(id.Email, "@")
	if !id.EmailVerified || at < 0 {
		return false
	}
	return slices.Contains(c.domains, strings.ToLower(id.Email[at+1:]))
}

// parseVerified reads email_verified, which some providers send as the
// string "true" instead of a boolean.
func parseVerified(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.EqualFold(s, "true")
}

// randomString returns 32 random bytes, base64url-encoded.
func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidc: generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
