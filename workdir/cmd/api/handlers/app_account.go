package handlers

import (
	"net/http"

	apperrors "app/internal/errors"
	"app/internal/pkg/accounts"
	"app/internal/pkg/tenants"
)

// msgPasswordMismatch is the error of a password change whose confirmation
// differs from the new password.
const msgPasswordMismatch = "the new password and its confirmation do not match"

// appAccountPage is the data rendered by templates/account.html.
type appAccountPage struct {
	appLayout
	// Workspace is the viewer's tenant; nil when they have none. Its API
	// key is never rendered: the page fetches it on demand from
	// GET /tenants/me.
	Workspace *tenants.Tenant
	// Confirmation is what the viewer types to delete their account (see
	// accounts.ConfirmationFor).
	Confirmation string
	// CanDelete tells whether account deletion is available.
	CanDelete bool
	// Notice confirms the change that led back to the page, if any.
	Notice string
}

// Account renders the settings of the signed-in user: their profile, email
// and password, the API key of their workspace and the account deletion.
func (c *appController) Account(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	page := appAccountPage{appLayout: appLayout{Active: "account"}, Confirmation: accounts.ConfirmationFor(*u), CanDelete: c.accounts != nil}
	if r.URL.Query().Get("email") == "changed" && u.Email != "" {
		page.Notice = "Email changed. Open the link we sent to " + u.Email + " to verify it."
	}
	t, err := c.tenants.GetByOwner(r.Context(), u.ID)
	switch {
	case err == nil:
		page.Workspace = &t
	case !isNotFound(err):
		renderAppError(w, r, err)
		return
	}
	renderAppPage(w, r, appAccountTemplate, &page)
}

// changeEmailRequest is the body of POST /app/account/email.
type changeEmailRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

// ChangeEmail replaces the viewer's email once their current password is
// checked and sends a verification link to the new address, which stays
// unverified until it is redeemed. Answers the updated user as JSON; the
// page then reloads as /app/account?email=changed to confirm it.
func (c *appController) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apperrors.WriteHTTP(w, r, apperrors.NewForbidden("cross-origin request"))
		return
	}
	var req changeEmailRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	u, err := c.auth.ChangeEmail(r.Context(), userFrom(r.Context()).ID, req.CurrentPassword, req.Email)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toUserResponse(u))
}

// changePasswordRequest is the body of POST /app/account/password.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	// ConfirmPassword must repeat NewPassword.
	ConfirmPassword string `json:"confirm_password"`
}

// ChangePassword sets a new password on the viewer's account once their
// current password is checked. The session stays signed in.
func (c *appController) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apperrors.WriteHTTP(w, r, apperrors.NewForbidden("cross-origin request"))
		return
	}
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest(msgPasswordMismatch))
		return
	}
	if err := c.auth.ChangePassword(r.Context(), userFrom(r.Context()).ID, req.CurrentPassword, req.NewPassword); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccountRequest is the body of POST /app/account/delete.
type deleteAccountRequest struct {
	// Confirmation must be the viewer's email (their username when they
	// have none).
	Confirmation string `json:"confirmation"`
}

// DeleteAccount cancels the Stripe subscription of the viewer's workspace,
// deletes the workspace with its forms, submissions and files, then the
// account, and clears the session cookie; the page then leads to /.
func (c *appController) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apperrors.WriteHTTP(w, r, apperrors.NewForbidden("cross-origin request"))
		return
	}
	if c.accounts == nil {
		apperrors.WriteHTTP(w, r, apperrors.NewNotFound("account deletion is not available"))
		return
	}
	var req deleteAccountRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := c.accounts.DeleteAccount(r.Context(), *userFrom(r.Context()), req.Confirmation); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}
