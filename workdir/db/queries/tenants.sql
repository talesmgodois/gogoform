-- name: CreateTenant :one
INSERT INTO tenants (name, api_key, owner_user_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTenantByAPIKey :one
SELECT * FROM tenants
WHERE api_key = $1 AND is_active;

-- name: GetTenantByID :one
SELECT * FROM tenants
WHERE id = $1 AND is_active;

-- name: GetTenantByOwner :one
SELECT * FROM tenants
WHERE owner_user_id = sqlc.arg(owner_user_id)::integer AND is_active;

-- name: RotateTenantAPIKey :one
UPDATE tenants
SET api_key = sqlc.arg(new_key)
WHERE id = sqlc.arg(id) AND is_active
RETURNING *;

-- name: GetTenantByStripeCustomerID :one
SELECT * FROM tenants
WHERE stripe_customer_id = sqlc.arg(stripe_customer_id)::text AND is_active;

-- name: SetTenantStripeCustomer :one
UPDATE tenants
SET stripe_customer_id = sqlc.arg(stripe_customer_id)::text
WHERE id = sqlc.arg(id) AND is_active
RETURNING *;

-- name: LinkTenantSubscription :one
-- Links the tenant of a completed checkout to its subscription. Status,
-- period end and event time are kept when that subscription is already
-- stored, so a subscription event applied first is not overwritten, and
-- cleared otherwise. The plan is left to the subscription events.
UPDATE tenants
SET subscription_status = CASE WHEN stripe_subscription_id = sqlc.arg(stripe_subscription_id)::text THEN subscription_status END,
    current_period_end = CASE WHEN stripe_subscription_id = sqlc.arg(stripe_subscription_id)::text THEN current_period_end END,
    subscription_event_at = CASE WHEN stripe_subscription_id = sqlc.arg(stripe_subscription_id)::text THEN subscription_event_at END,
    stripe_subscription_id = sqlc.arg(stripe_subscription_id)::text
WHERE stripe_customer_id = sqlc.arg(stripe_customer_id)::text AND is_active
RETURNING *;

-- name: UpdateTenantSubscriptionByCustomer :one
-- An update about the stored subscription created before the last one
-- applied matches no row, so events delivered out of order are dropped.
UPDATE tenants
SET stripe_subscription_id = sqlc.narg(stripe_subscription_id),
    subscription_status = sqlc.narg(subscription_status),
    current_period_end = sqlc.narg(current_period_end),
    subscription_event_at = sqlc.arg(event_at)::timestamp,
    plan = sqlc.arg(plan)
WHERE stripe_customer_id = sqlc.arg(stripe_customer_id)::text AND is_active
  AND (stripe_subscription_id IS DISTINCT FROM sqlc.narg(stripe_subscription_id)
       OR subscription_event_at IS NULL
       OR subscription_event_at <= sqlc.arg(event_at)::timestamp)
RETURNING *;
