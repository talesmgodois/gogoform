package auth

import (
	"context"
	"testing"
	"time"

	apperrors "app/internal/errors"
)

// memTokens is an in-memory TokenRepository, keyed by the hash of each
// token. now, when set, is used instead of time.Now for expiry checks.
type memTokens struct {
	rows map[string]UserToken
	now  func() time.Time
}

func newMemTokens(now func() time.Time) *memTokens {
	if now == nil {
		now = time.Now
	}
	return &memTokens{rows: map[string]UserToken{}, now: now}
}

func (m *memTokens) Create(_ context.Context, userID int32, purpose TokenPurpose, hash []byte, expiresAt time.Time) error {
	m.rows[string(hash)] = UserToken{UserID: userID, Purpose: purpose, ExpiresAt: expiresAt, CreatedAt: m.now()}
	return nil
}

func (m *memTokens) Consume(_ context.Context, hash []byte, purpose TokenPurpose) (UserToken, error) {
	ut, ok := m.rows[string(hash)]
	if !ok || ut.Purpose != purpose || ut.UsedAt != nil || !ut.ExpiresAt.After(m.now()) {
		return UserToken{}, apperrors.NewNotFound(msgTokenNotFound)
	}
	used := m.now()
	ut.UsedAt = &used
	m.rows[string(hash)] = ut
	return ut, nil
}

func (m *memTokens) GetByHash(_ context.Context, hash []byte) (UserToken, error) {
	ut, ok := m.rows[string(hash)]
	if !ok {
		return UserToken{}, apperrors.NewNotFound(msgTokenNotFound)
	}
	return ut, nil
}

func (m *memTokens) Invalidate(_ context.Context, userID int32, purpose TokenPurpose) error {
	used := m.now()
	for k, ut := range m.rows {
		if ut.UserID == userID && ut.Purpose == purpose && ut.UsedAt == nil {
			ut.UsedAt = &used
			m.rows[k] = ut
		}
	}
	return nil
}

func TestTokenLifecycleValid(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	plaintext, err := issueToken(ctx, tokens, 1, TokenPurposeEmailVerification, EmailVerificationTokenTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	ut, reason, err := consumeToken(ctx, tokens, plaintext, TokenPurposeEmailVerification, now)
	if err != nil || reason != reasonNone || ut.UserID != 1 || ut.Purpose != TokenPurposeEmailVerification {
		t.Fatalf("consumeToken = %+v, %q, %v", ut, reason, err)
	}
}

func TestTokenLifecycleExpired(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	plaintext, err := issueToken(ctx, tokens, 1, TokenPurposePasswordReset, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute) // past expiry
	_, reason, err := consumeToken(ctx, tokens, plaintext, TokenPurposePasswordReset, now)
	assertCode(t, err, apperrors.CodeUnauthorized)
	if appErr, _ := apperrors.As(err); appErr.Message != msgInvalidToken {
		t.Fatalf("message = %q, want %q", appErr.Message, msgInvalidToken)
	}
	if reason != reasonExpired {
		t.Fatalf("reason = %q, want %q", reason, reasonExpired)
	}
}

func TestTokenLifecycleReused(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	plaintext, err := issueToken(ctx, tokens, 1, TokenPurposeEmailVerification, EmailVerificationTokenTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := consumeToken(ctx, tokens, plaintext, TokenPurposeEmailVerification, now); err != nil {
		t.Fatal(err)
	}
	_, reason, err := consumeToken(ctx, tokens, plaintext, TokenPurposeEmailVerification, now)
	assertCode(t, err, apperrors.CodeUnauthorized)
	if reason != reasonAlreadyUsed {
		t.Fatalf("reason = %q, want %q", reason, reasonAlreadyUsed)
	}
}

func TestTokenLifecycleWrongPurpose(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	plaintext, err := issueToken(ctx, tokens, 1, TokenPurposeEmailVerification, EmailVerificationTokenTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	_, reason, err := consumeToken(ctx, tokens, plaintext, TokenPurposePasswordReset, now)
	assertCode(t, err, apperrors.CodeUnauthorized)
	if reason != reasonWrongPurpose {
		t.Fatalf("reason = %q, want %q", reason, reasonWrongPurpose)
	}
}

func TestTokenLifecycleUnknown(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	_, reason, err := consumeToken(ctx, tokens, "does-not-exist", TokenPurposeEmailVerification, now)
	assertCode(t, err, apperrors.CodeUnauthorized)
	if reason != reasonUnknownToken {
		t.Fatalf("reason = %q, want %q", reason, reasonUnknownToken)
	}
}

func TestTokenLifecycleInvalidatesOlderTokens(t *testing.T) {
	now := time.Now()
	tokens := newMemTokens(func() time.Time { return now })
	ctx := context.Background()

	first, err := issueToken(ctx, tokens, 1, TokenPurposePasswordReset, PasswordResetTokenTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := issueToken(ctx, tokens, 1, TokenPurposePasswordReset, PasswordResetTokenTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("issueToken returned the same plaintext twice")
	}
	// The older token was invalidated by the second issueToken call.
	_, reason, err := consumeToken(ctx, tokens, first, TokenPurposePasswordReset, now)
	assertCode(t, err, apperrors.CodeUnauthorized)
	if reason != reasonAlreadyUsed {
		t.Fatalf("reason = %q, want %q", reason, reasonAlreadyUsed)
	}
	// The latest token still redeems.
	if _, _, err := consumeToken(ctx, tokens, second, TokenPurposePasswordReset, now); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	a, hashA, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, hashB, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("generateToken returned the same plaintext twice")
	}
	if string(hashA) == string(hashB) {
		t.Fatal("generateToken returned the same hash twice")
	}
	if string(hashToken(a)) != string(hashA) {
		t.Fatal("hashToken is not deterministic")
	}
}
