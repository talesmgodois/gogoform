-- InsertStripeEvent affects 0 rows when the event id was already recorded.
-- name: InsertStripeEvent :execrows
INSERT INTO stripe_events (id, type)
VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING;
