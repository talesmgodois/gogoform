# 000024 stripe_billing

Plans: `free` (default) and `pro` at 9 USD/month, billed with Stripe. Depends on 000021.

## DB
- On `tenants`: `plan` (enum free|pro, default free), `stripe_customer_id`, `stripe_subscription_id`, `subscription_status`, `current_period_end`.
- Table `stripe_events(id text PK, type, received_at)` for webhook idempotency.

## Backend (use github.com/stripe/stripe-go)
- Config/env: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID`, `APP_BASE_URL`. Billing routes are disabled (404) when Stripe is not configured.
- `POST /billing/checkout` (signed in): creates/reuses the Stripe customer and a subscription Checkout Session for `STRIPE_PRICE_ID`; redirects to Stripe.
- `POST /billing/portal` (signed in): creates a Customer Portal session.
- `POST /webhooks/stripe`: verifies the signature, ignores already processed event ids, handles `checkout.session.completed`, `customer.subscription.created|updated|deleted` and `invoice.payment_failed`; sets plan `pro` while status is active/trialing, back to `free` when canceled/unpaid.
- Page `/app/billing`: current plan, renewal date, Upgrade (checkout) or Manage (portal) button, success/cancel return messages.

## Tests
- Webhook handler tests with signed fake payloads (use stripe-go's webhook test helpers), idempotency, plan transitions.
