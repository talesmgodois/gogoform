package auth

import (
	"context"
	"testing"
	"time"

	apperrors "app/internal/errors"
)

func TestChangePassword(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	u := signUp(t, env.svc, "alice@example.com", "correct horse")
	if err := env.svc.RequestPasswordReset(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	resetToken := env.notifier.resets[0].token

	err := env.svc.ChangePassword(ctx, u.ID, "wrong password", "a whole new password")
	assertCode(t, err, apperrors.CodeInvalidArgument)
	err = env.svc.ChangePassword(ctx, u.ID, "correct horse", "short")
	assertCode(t, err, apperrors.CodeInvalidArgument)

	if err := env.svc.ChangePassword(ctx, u.ID, "correct horse", "a whole new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.CheckPassword(ctx, "alice@example.com", "correct horse"); err == nil {
		t.Fatal("old password should no longer work")
	}
	if _, err := env.svc.CheckPassword(ctx, "alice@example.com", "a whole new password"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
	// A reset link sent before the change no longer works.
	err = env.svc.ResetPassword(ctx, resetToken, "yet another password")
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestChangeEmail(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	u := signUp(t, env.svc, "alice@example.com", "correct horse")
	signUp(t, env.svc, "bob@example.com", "correct horse")
	if _, err := env.users.MarkEmailVerified(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.RequestPasswordReset(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	resetToken := env.notifier.resets[0].token
	sent := len(env.notifier.verifications)

	_, err := env.svc.ChangeEmail(ctx, u.ID, "wrong password", "alice@new.example.com")
	assertCode(t, err, apperrors.CodeInvalidArgument)
	_, err = env.svc.ChangeEmail(ctx, u.ID, "correct horse", "not an email")
	assertCode(t, err, apperrors.CodeInvalidArgument)
	_, err = env.svc.ChangeEmail(ctx, u.ID, "correct horse", "Alice@example.com")
	assertCode(t, err, apperrors.CodeInvalidArgument)
	_, err = env.svc.ChangeEmail(ctx, u.ID, "correct horse", "bob@example.com")
	assertCode(t, err, apperrors.CodeConflict)
	if len(env.notifier.verifications) != sent {
		t.Fatal("a rejected change should send no verification email")
	}

	got, err := env.svc.ChangeEmail(ctx, u.ID, "correct horse", " Alice@New.example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "alice@new.example.com" || got.EmailVerified() {
		t.Fatalf("user = %+v, want the new email, unverified", got)
	}
	if len(env.notifier.verifications) != sent+1 {
		t.Fatalf("verifications = %d, want %d", len(env.notifier.verifications), sent+1)
	}
	last := env.notifier.verifications[sent]
	if last.user.Email != "alice@new.example.com" {
		t.Fatalf("verification sent to %q, want the new address", last.user.Email)
	}
	if _, err := env.svc.VerifyEmail(ctx, last.token); err != nil {
		t.Fatalf("the new verification link should work: %v", err)
	}
	if !env.users.users[0].EmailVerified() {
		t.Fatal("VerifyEmail should verify the new address")
	}
	// A reset link sent to the old address no longer works.
	err = env.svc.ResetPassword(ctx, resetToken, "yet another password")
	assertCode(t, err, apperrors.CodeUnauthorized)
}
