package billing

import (
	"github.com/stripe/stripe-go/v82"

	"app/internal/pkg/tenants"
)

// PlanForStatus returns the plan a tenant on current gets for a subscription
// in status. Active and trialing subscriptions grant pro; canceled, unpaid,
// incomplete_expired and paused ones fall back to free. Any other status
// (past_due, incomplete, unknown) keeps current, so a pending payment retry
// neither grants nor revokes access.
func PlanForStatus(status stripe.SubscriptionStatus, current tenants.Plan) tenants.Plan {
	switch status {
	case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing:
		return tenants.PlanPro
	case stripe.SubscriptionStatusCanceled, stripe.SubscriptionStatusUnpaid,
		stripe.SubscriptionStatusIncompleteExpired, stripe.SubscriptionStatusPaused:
		return tenants.PlanFree
	default:
		return current
	}
}

// IsEndedStatus reports whether a subscription in status has ended for good:
// Stripe never reactivates a canceled or incomplete_expired subscription.
func IsEndedStatus(status stripe.SubscriptionStatus) bool {
	return status == stripe.SubscriptionStatusCanceled || status == stripe.SubscriptionStatusIncompleteExpired
}
