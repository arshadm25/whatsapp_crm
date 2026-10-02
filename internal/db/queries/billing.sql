-- name: ListPlans :many
SELECT * FROM plans
WHERE (NOT @active_only::boolean OR is_active)
ORDER BY sort_order, price_minor, code;

-- name: GetPlan :one
SELECT * FROM plans WHERE code = $1;

-- name: GetPlanByRazorpayID :one
SELECT * FROM plans WHERE razorpay_plan_id = $1;

-- name: UpsertPlan :one
INSERT INTO plans (code, name, price_minor, included_numbers, included_seats, extra_seat_minor,
                   razorpay_plan_id, sort_order, is_active)
VALUES (@code, @name, @price_minor, @included_numbers, @included_seats, @extra_seat_minor,
        sqlc.narg(razorpay_plan_id), @sort_order, @is_active)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name, price_minor = EXCLUDED.price_minor, included_numbers = EXCLUDED.included_numbers,
    included_seats = EXCLUDED.included_seats, extra_seat_minor = EXCLUDED.extra_seat_minor,
    razorpay_plan_id = EXCLUDED.razorpay_plan_id, sort_order = EXCLUDED.sort_order,
    is_active = EXCLUDED.is_active, updated_at = now()
RETURNING *;

-- name: GetSubscription :one
-- Run inside the tenant.
SELECT * FROM subscriptions LIMIT 1;

-- name: GetSubscriptionForUpdate :one
SELECT * FROM subscriptions LIMIT 1 FOR UPDATE;

-- name: SetProviderSubscription :exec
UPDATE subscriptions
SET provider_customer_id = @provider_customer_id, provider_subscription_id = @provider_subscription_id, updated_at = now()
WHERE tenant_id = @tenant_id;

-- name: ApplySubscriptionState :one
UPDATE subscriptions
SET status = @status,
    plan_code = coalesce(sqlc.narg(plan_code), plan_code),
    current_period_start = coalesce(sqlc.narg(period_start), current_period_start),
    current_period_end = coalesce(sqlc.narg(period_end), current_period_end),
    cancel_at_period_end = @cancel_at_period_end,
    updated_at = now()
WHERE tenant_id = @tenant_id
RETURNING *;

-- name: SetCancelAtPeriodEnd :exec
UPDATE subscriptions SET cancel_at_period_end = @cancel, updated_at = now() WHERE tenant_id = @tenant_id;

-- name: ExtendTrial :one
UPDATE subscriptions SET current_period_end = @until, updated_at = now()
WHERE tenant_id = @tenant_id AND status = 'trialing'
RETURNING *;

-- name: SubscriptionTenant :one
SELECT t.tenant_id::uuid FROM (SELECT subscription_tenant(@provider_subscription_id::text) AS tenant_id) t
WHERE t.tenant_id IS NOT NULL;

-- name: RecordRazorpayEvent :execrows
INSERT INTO razorpay_events (id, event) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: BillingUsage :one
-- Run inside the tenant: what the plan's limits are measured against.
SELECT (SELECT count(*) FROM phone_numbers WHERE status = 'connected')::int AS connected_numbers,
       (SELECT count(*) FROM memberships)::int AS seats;

-- name: DropTrialPlan :exec
-- A plan chosen during the trial was cancelled before its first charge.
UPDATE subscriptions SET plan_code = NULL, provider_subscription_id = NULL, updated_at = now()
WHERE tenant_id = @tenant_id AND status = 'trialing';
