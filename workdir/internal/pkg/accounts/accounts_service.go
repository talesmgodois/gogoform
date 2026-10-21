package accounts

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/stripe/stripe-go/v82"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/billing"
	"app/internal/pkg/tenants"
)

// Client-facing messages of the errors returned by Service.
const (
	msgWrongConfirmation = "type your email address to confirm the deletion"
	msgCannotCancel      = "your subscription could not be cancelled; contact support to close your account"
)

// Options configures a Service.
type Options struct {
	// Tx runs the deletion in one transaction; required.
	Tx database.TxRunner
	// StoreFor returns the Store to use inside a Tx transaction; NewStore
	// when nil.
	StoreFor func(q db.Querier) Store
	// Subscriptions cancels the workspace's subscription; nil when billing
	// is not configured.
	Subscriptions Subscriptions
}

// Service deletes user accounts.
type Service struct {
	workspaces    Workspaces
	tx            database.TxRunner
	storeFor      func(q db.Querier) Store
	subscriptions Subscriptions
}

// NewService returns a Service finding the workspace of a user in
// workspaces.
func NewService(workspaces Workspaces, opts Options) (*Service, error) {
	if opts.Tx == nil {
		return nil, errors.New("accounts: transaction runner is required")
	}
	if opts.StoreFor == nil {
		opts.StoreFor = func(q db.Querier) Store { return NewStore(q) }
	}
	return &Service{workspaces: workspaces, tx: opts.Tx, storeFor: opts.StoreFor, subscriptions: opts.Subscriptions}, nil
}

// ConfirmationFor is what user must type to delete their account: their
// email, or their username when they sign in without one.
func ConfirmationFor(user auth.User) string {
	if user.Email != "" {
		return user.Email
	}
	return user.Username
}

// DeleteAccount deletes user and the workspace they own once confirmation
// matches ConfirmationFor (case-insensitively), and an InvalidArgument
// otherwise. The workspace's Stripe subscription is cancelled first: when
// that fails nothing is deleted, so the owner is never billed for a
// workspace that no longer exists. Then the tenant data and the user are
// deleted in one transaction.
func (s *Service) DeleteAccount(ctx context.Context, user auth.User, confirmation string) error {
	if !strings.EqualFold(strings.TrimSpace(confirmation), ConfirmationFor(user)) {
		return apperrors.NewBadRequest(msgWrongConfirmation)
	}
	tenant, err := s.workspaces.GetByOwner(ctx, user.ID)
	hasTenant := err == nil
	if err != nil && !isNotFound(err) {
		return err
	}
	if hasTenant {
		if err := s.cancelSubscription(ctx, tenant); err != nil {
			return err
		}
	}
	err = s.tx.InTx(ctx, func(q db.Querier) error {
		store := s.storeFor(q)
		if hasTenant {
			if err := store.DeleteTenantData(ctx, tenant.ID); err != nil {
				return err
			}
		}
		return store.DeleteUser(ctx, user.ID)
	})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "accounts: account deleted", "user_id", user.ID, "had_workspace", hasTenant)
	return nil
}

// cancelSubscription cancels the ongoing subscription of tenant. Without
// billing configured such a subscription cannot be cancelled, so the
// deletion is refused rather than leaving it running.
func (s *Service) cancelSubscription(ctx context.Context, tenant tenants.Tenant) error {
	if s.subscriptions != nil {
		return s.subscriptions.CancelSubscription(ctx, tenant)
	}
	if tenant.StripeSubscriptionID != "" && !billing.IsEndedStatus(stripe.SubscriptionStatus(tenant.SubscriptionStatus)) {
		return apperrors.New(apperrors.CodeConflict, msgCannotCancel)
	}
	return nil
}

func isNotFound(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeNotFound
}
