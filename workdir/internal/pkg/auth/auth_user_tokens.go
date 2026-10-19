package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// TokenPurpose is what a single-use user token authorizes.
type TokenPurpose string

const (
	TokenPurposeEmailVerification TokenPurpose = "email_verification"
	TokenPurposePasswordReset     TokenPurpose = "password_reset"
)

// Token TTLs; see issueToken.
const (
	EmailVerificationTokenTTL = 24 * time.Hour
	PasswordResetTokenTTL     = time.Hour
)

// tokenByteLen is the size of the random value encoded into a plaintext
// token: 256 bits.
const tokenByteLen = 32

// msgTokenNotFound is the store-level message of a token that does not
// exist, or no longer matches the purpose, expiry or used_at a caller
// required; it never reaches a client (see consumeToken).
const msgTokenNotFound = "token not found"

// UserToken is a row of user_tokens, as needed to check and consume it. The
// plaintext token is never stored; only its SHA-256 hash is.
type UserToken struct {
	ID        int32
	UserID    int32
	Purpose   TokenPurpose
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// TokenRepository persists the single-use tokens behind email verification
// and password reset.
//
// Errors are AppErrors from internal/errors: CodeNotFound when no token
// matches (Consume also reports CodeNotFound for an expired, already-used or
// wrong-purpose token, indistinguishable from an unknown one at the query
// level) and CodeInternal for anything else.
type TokenRepository interface {
	// Create stores a new token for userID, identified by hash, valid until
	// expiresAt.
	Create(ctx context.Context, userID int32, purpose TokenPurpose, hash []byte, expiresAt time.Time) error
	// Consume atomically marks the token matching hash and purpose as used,
	// but only if it is unused and unexpired.
	Consume(ctx context.Context, hash []byte, purpose TokenPurpose) (UserToken, error)
	// GetByHash returns the token matching hash whatever its state, used only
	// to diagnose why Consume rejected it.
	GetByHash(ctx context.Context, hash []byte) (UserToken, error)
	// Invalidate marks every unused token of userID and purpose as used, so
	// issuing a new one revokes the old ones.
	Invalidate(ctx context.Context, userID int32, purpose TokenPurpose) error
}

var _ TokenRepository = (*UserTokenStore)(nil)

// UserTokenStore implements TokenRepository on top of the sqlc queries.
type UserTokenStore struct {
	q db.Querier
}

// NewUserTokenStore returns a UserTokenStore that runs its queries through q.
func NewUserTokenStore(q db.Querier) *UserTokenStore {
	return &UserTokenStore{q: q}
}

func (s *UserTokenStore) Create(ctx context.Context, userID int32, purpose TokenPurpose, hash []byte, expiresAt time.Time) error {
	_, err := s.q.CreateUserToken(ctx, db.CreateUserTokenParams{
		UserID: userID, Purpose: db.UserTokenPurpose(purpose), TokenHash: hash, ExpiresAt: expiresAt,
	})
	if err != nil {
		return apperrors.NewInternal(err)
	}
	return nil
}

func (s *UserTokenStore) Consume(ctx context.Context, hash []byte, purpose TokenPurpose) (UserToken, error) {
	row, err := s.q.ConsumeUserToken(ctx, db.ConsumeUserTokenParams{TokenHash: hash, Purpose: db.UserTokenPurpose(purpose)})
	if err != nil {
		return UserToken{}, mapTokenError(err)
	}
	return toUserToken(row), nil
}

func (s *UserTokenStore) GetByHash(ctx context.Context, hash []byte) (UserToken, error) {
	row, err := s.q.GetUserTokenByHash(ctx, hash)
	if err != nil {
		return UserToken{}, mapTokenError(err)
	}
	return toUserToken(row), nil
}

func (s *UserTokenStore) Invalidate(ctx context.Context, userID int32, purpose TokenPurpose) error {
	if err := s.q.InvalidateUserTokens(ctx, db.InvalidateUserTokensParams{UserID: userID, Purpose: db.UserTokenPurpose(purpose)}); err != nil {
		return apperrors.NewInternal(err)
	}
	return nil
}

func mapTokenError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NewNotFound(msgTokenNotFound)
	}
	return apperrors.NewInternal(err)
}

func toUserToken(row db.UserToken) UserToken {
	return UserToken{
		ID:        row.ID,
		UserID:    row.UserID,
		Purpose:   TokenPurpose(row.Purpose),
		ExpiresAt: row.ExpiresAt,
		UsedAt:    row.UsedAt,
		CreatedAt: row.CreatedAt,
	}
}

// generateToken returns a new random, URL-safe plaintext token together with
// the SHA-256 hash that is the only thing ever persisted.
func generateToken() (plaintext string, hash []byte, err error) {
	b := make([]byte, tokenByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

// hashToken returns the SHA-256 hash of a plaintext token, as stored and
// looked up.
func hashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// issueToken invalidates any older unused tokens of userID for purpose, then
// generates, stores and returns a fresh one valid for ttl.
func issueToken(ctx context.Context, tokens TokenRepository, userID int32, purpose TokenPurpose, ttl time.Duration, now time.Time) (string, error) {
	if err := tokens.Invalidate(ctx, userID, purpose); err != nil {
		return "", err
	}
	plaintext, hash, err := generateToken()
	if err != nil {
		return "", apperrors.NewInternal(err)
	}
	if err := tokens.Create(ctx, userID, purpose, hash, now.Add(ttl)); err != nil {
		return "", err
	}
	return plaintext, nil
}

// tokenFailureReason explains, for logs and tests only, why consumeToken
// rejected a token; it is never shown to the client, which always gets
// msgInvalidToken.
type tokenFailureReason string

const (
	reasonNone         tokenFailureReason = ""
	reasonUnknownToken tokenFailureReason = "unknown token"
	reasonWrongPurpose tokenFailureReason = "wrong purpose"
	reasonAlreadyUsed  tokenFailureReason = "already used"
	reasonExpired      tokenFailureReason = "expired"
)

// consumeToken redeems plaintext for purpose. On success it returns the
// consumed UserToken; on failure it returns a client-facing
// apperrors.CodeUnauthorized "invalid or expired token" error and, for the
// caller's logs or tests, the specific internal reason.
func consumeToken(ctx context.Context, tokens TokenRepository, plaintext string, purpose TokenPurpose, now time.Time) (UserToken, tokenFailureReason, error) {
	hash := hashToken(plaintext)
	ut, err := tokens.Consume(ctx, hash, purpose)
	if err == nil {
		return ut, reasonNone, nil
	}
	if !isNotFound(err) {
		return UserToken{}, reasonNone, err
	}
	reason := reasonUnknownToken
	if existing, getErr := tokens.GetByHash(ctx, hash); getErr == nil {
		switch {
		case existing.Purpose != purpose:
			reason = reasonWrongPurpose
		case existing.UsedAt != nil:
			reason = reasonAlreadyUsed
		case !existing.ExpiresAt.After(now):
			reason = reasonExpired
		}
	}
	return UserToken{}, reason, apperrors.NewUnauthorized(msgInvalidToken)
}
