package billing

import (
	"testing"

	"github.com/stripe/stripe-go/v82"

	"app/internal/pkg/tenants"
)

func TestPlanForStatus(t *testing.T) {
	tests := []struct {
		status  stripe.SubscriptionStatus
		current tenants.Plan
		want    tenants.Plan
	}{
		{stripe.SubscriptionStatusActive, tenants.PlanFree, tenants.PlanPro},
		{stripe.SubscriptionStatusActive, tenants.PlanPro, tenants.PlanPro},
		{stripe.SubscriptionStatusTrialing, tenants.PlanFree, tenants.PlanPro},
		{stripe.SubscriptionStatusCanceled, tenants.PlanPro, tenants.PlanFree},
		{stripe.SubscriptionStatusUnpaid, tenants.PlanPro, tenants.PlanFree},
		{stripe.SubscriptionStatusIncompleteExpired, tenants.PlanPro, tenants.PlanFree},
		{stripe.SubscriptionStatusPaused, tenants.PlanPro, tenants.PlanFree},
		{stripe.SubscriptionStatusPastDue, tenants.PlanPro, tenants.PlanPro},
		{stripe.SubscriptionStatusPastDue, tenants.PlanFree, tenants.PlanFree},
		{stripe.SubscriptionStatusIncomplete, tenants.PlanFree, tenants.PlanFree},
		{stripe.SubscriptionStatusIncomplete, tenants.PlanPro, tenants.PlanPro},
		{"", tenants.PlanPro, tenants.PlanPro},
		{"something_new", tenants.PlanFree, tenants.PlanFree},
	}
	for _, tt := range tests {
		t.Run(string(tt.status)+"/"+string(tt.current), func(t *testing.T) {
			if got := PlanForStatus(tt.status, tt.current); got != tt.want {
				t.Fatalf("PlanForStatus(%q, %q) = %q, want %q", tt.status, tt.current, got, tt.want)
			}
		})
	}
}

func TestIsEndedStatus(t *testing.T) {
	for status, want := range map[stripe.SubscriptionStatus]bool{
		stripe.SubscriptionStatusCanceled:          true,
		stripe.SubscriptionStatusIncompleteExpired: true,
		stripe.SubscriptionStatusActive:            false,
		stripe.SubscriptionStatusPastDue:           false,
		stripe.SubscriptionStatusUnpaid:            false,
		stripe.SubscriptionStatusIncomplete:        false,
		"":                                         false,
	} {
		if got := IsEndedStatus(status); got != want {
			t.Errorf("IsEndedStatus(%q) = %v, want %v", status, got, want)
		}
	}
}
