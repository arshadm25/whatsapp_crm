-- name: ListPlans :many
SELECT * FROM plans
WHERE (NOT @active_only::boolean OR is_active)
ORDER BY sort_order, price_minor, code;

-- name: GetPlan :one
SELECT * FROM plans WHERE code = $1;

-- name: GetPlanByRazorpayID :one
-- The Razorpay plan may be the plan's monthly or yearly one.
SELECT * FROM plans WHERE razorpay_plan_id = @razorpay_plan_id::text OR yearly_razorpay_plan_id = @razorpay_plan_id::text;

-- name: UpsertPlan :one
INSERT INTO plans (code, name, price_minor, included_numbers, included_seats, extra_seat_minor,
                   razorpay_plan_id, extra_seat_razorpay_plan_id, sort_order, is_active, ai_replies_per_month,
                   contacts_limit, messages_per_month, yearly_price_minor, yearly_razorpay_plan_id,
                   tagline, highlights, is_popular)
VALUES (@code, @name, @price_minor, @included_numbers, @included_seats, @extra_seat_minor,
        sqlc.narg(razorpay_plan_id), sqlc.narg(extra_seat_razorpay_plan_id), @sort_order, @is_active, @ai_replies_per_month,
        sqlc.narg(contacts_limit), sqlc.narg(messages_per_month), sqlc.narg(yearly_price_minor), sqlc.narg(yearly_razorpay_plan_id),
        @tagline, @highlights::text[], @is_popular)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name, price_minor = EXCLUDED.price_minor, included_numbers = EXCLUDED.included_numbers,
    included_seats = EXCLUDED.included_seats, extra_seat_minor = EXCLUDED.extra_seat_minor,
    razorpay_plan_id = EXCLUDED.razorpay_plan_id, extra_seat_razorpay_plan_id = EXCLUDED.extra_seat_razorpay_plan_id,
    sort_order = EXCLUDED.sort_order,
    is_active = EXCLUDED.is_active, ai_replies_per_month = EXCLUDED.ai_replies_per_month,
    contacts_limit = EXCLUDED.contacts_limit, messages_per_month = EXCLUDED.messages_per_month,
    yearly_price_minor = EXCLUDED.yearly_price_minor, yearly_razorpay_plan_id = EXCLUDED.yearly_razorpay_plan_id,
    tagline = EXCLUDED.tagline, highlights = EXCLUDED.highlights, is_popular = EXCLUDED.is_popular, updated_at = now()
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
    billing_interval = coalesce(sqlc.narg(billing_interval), billing_interval),
    -- A plan Razorpay reports replaces one granted from the admin console.
    admin_granted = admin_granted AND sqlc.narg(plan_code)::text IS NULL,
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

-- name: CurrentPlan :one
-- Run inside the tenant. No row while the workspace is on a trial with no plan chosen.
SELECT p.* FROM plans p JOIN subscriptions s ON s.plan_code = p.code;

-- name: NumbersInUse :one
-- Numbers that count against the plan: connected or being connected, other than the one named.
SELECT count(*)::int FROM phone_numbers
WHERE status IN ('pending', 'connected') AND phone_number_id <> @other_id::text;

-- name: SetSeatSubscription :exec
UPDATE subscriptions SET seat_provider_subscription_id = @seat_provider_subscription_id, updated_at = now()
WHERE tenant_id = @tenant_id;

-- name: SetExtraSeats :exec
-- A terminal seat event also forgets the seat subscription.
UPDATE subscriptions
SET extra_seats = @extra_seats,
    seat_provider_subscription_id = CASE WHEN @ended::boolean THEN NULL ELSE seat_provider_subscription_id END,
    updated_at = now()
WHERE tenant_id = @tenant_id;

-- name: SeatUsage :one
-- Run inside the tenant. No row while no plan is chosen (a trial has no seat limit).
SELECT (p.included_seats + s.extra_seats)::int AS seat_limit,
       (SELECT count(*) FROM memberships)::int AS members,
       (SELECT count(*) FROM invites WHERE accepted_at IS NULL AND expires_at > now())::int AS open_invites
FROM subscriptions s JOIN plans p ON p.code = s.plan_code;

-- name: StartFreePlan :one
-- The free plan needs no payment: it starts now and runs until the owner chooses another plan.
UPDATE subscriptions
SET status = 'active', plan_code = @plan_code, billing_interval = 'month', provider_subscription_id = NULL,
    cancel_at_period_end = false, admin_granted = false, current_period_start = @period_start, current_period_end = @period_end,
    updated_at = now()
WHERE tenant_id = @tenant_id
RETURNING *;

-- name: GrantPlan :one
-- Staff put the workspace on a plan without Razorpay. With an end date (cancel_at_period_end)
-- it stops at period_end; without one it runs until the owner pays for a plan.
UPDATE subscriptions
SET status = 'active', plan_code = @plan_code, billing_interval = 'month', provider_subscription_id = NULL,
    admin_granted = true, cancel_at_period_end = @has_end, current_period_start = @period_start,
    current_period_end = @period_end, updated_at = now()
WHERE tenant_id = @tenant_id
RETURNING *;

-- name: DropProviderSubscription :exec
-- A checkout started from the free plan ended unpaid; the workspace stays on the free plan.
UPDATE subscriptions SET provider_subscription_id = NULL, updated_at = now()
WHERE tenant_id = @tenant_id AND provider_subscription_id = @provider_subscription_id;

-- name: OutboundMessagesSince :one
-- Run inside the tenant: messages the workspace has sent (or is sending) since a time, for the
-- plan's monthly message limit. Failed sends do not count.
SELECT count(*)::int FROM messages
WHERE direction = 'outbound' AND status <> 'failed' AND created_at >= @since;

-- name: ContactsInUse :one
-- Run inside the tenant: contacts that count against the plan's contact limit.
SELECT count(*)::int FROM contacts;
