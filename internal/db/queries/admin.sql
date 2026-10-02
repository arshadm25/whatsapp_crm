-- name: AdminListTenants :many
SELECT * FROM tenants
WHERE (sqlc.narg(status)::tenant_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(search)::text IS NULL OR name ILIKE '%' || sqlc.narg(search) || '%' OR slug ILIKE '%' || sqlc.narg(search) || '%')
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: AdminSetTenantStatus :one
UPDATE tenants SET status = @status, suspended_reason = sqlc.narg(reason), updated_at = now() WHERE id = @id
RETURNING *;

-- name: AdminTenantCounts :one
-- Run inside the tenant: its size at a glance.
SELECT (SELECT count(*) FROM memberships)::int AS members,
       (SELECT count(*) FROM phone_numbers WHERE status = 'connected')::int AS connected_numbers,
       (SELECT count(*) FROM phone_numbers)::int AS numbers,
       (SELECT coalesce(sum(sent), 0) FROM usage_daily WHERE day > current_date - 30)::int AS sent_30d,
       (SELECT coalesce(sum(received), 0) FROM usage_daily WHERE day > current_date - 30)::int AS received_30d;

-- name: AdminLastMessageAt :one
SELECT created_at FROM messages ORDER BY created_at DESC LIMIT 1;

-- name: AdminTenantNumbers :many
SELECT p.id, p.display_phone_number, p.verified_name, p.status, p.quality_rating, p.messaging_limit_tier,
       p.is_coexistence, w.waba_id
FROM phone_numbers p JOIN whatsapp_accounts w ON w.id = p.whatsapp_account_id
ORDER BY p.created_at;

-- name: AdminWebhookHourly :many
-- Meta webhook deliveries per hour: received, processed, failed and still waiting.
SELECT date_trunc('hour', received_at)::timestamptz AS hour,
       count(*)::int AS received,
       (count(*) FILTER (WHERE processed_at IS NOT NULL AND error IS NULL))::int AS processed,
       (count(*) FILTER (WHERE error IS NOT NULL))::int AS failed,
       (count(*) FILTER (WHERE processed_at IS NULL))::int AS pending,
       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM processed_at - received_at))
                FILTER (WHERE processed_at IS NOT NULL), 0)::float8 AS p95_lag_seconds
FROM meta_webhook_events
WHERE received_at > @since
GROUP BY 1
ORDER BY 1;

-- name: AdminWebhookErrors :many
SELECT id, received_at, field, waba_id, tenant_id, error
FROM meta_webhook_events
WHERE error IS NOT NULL AND received_at > @since
ORDER BY received_at DESC
LIMIT 50;

-- name: AdminMetaErrors :many
SELECT * FROM meta_api_errors
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id))
  AND (sqlc.narg(since)::timestamptz IS NULL OR occurred_at > sqlc.narg(since))
  AND (sqlc.narg(code)::int IS NULL OR code = sqlc.narg(code))
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT @lim;

-- name: AdminAuditLog :many
SELECT a.*, u.email AS actor_email
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR a.tenant_id = sqlc.narg(tenant_id))
  AND (sqlc.narg(actor_type)::actor_type IS NULL OR a.actor_type = sqlc.narg(actor_type))
  AND (sqlc.narg(before_id)::bigint IS NULL OR a.id < sqlc.narg(before_id))
ORDER BY a.id DESC
LIMIT @lim;

-- name: InsertAdminAudit :exec
INSERT INTO audit_log (tenant_id, actor_type, actor_id, action, target_type, target_id, reason, ip, metadata)
VALUES ($1, 'platform_admin', $2, $3, $4, $5, $6, $7, '{}');

-- name: AdminDeliveryStats :many
-- Client webhook deliveries in the tenant since a time, by status.
SELECT status, count(*)::int AS n FROM webhook_deliveries WHERE created_at > @since GROUP BY status;

-- name: AdminTenantOverview :many
SELECT o.tenant_id::uuid AS tenant_id, coalesce(o.waba_id, '')::text AS waba_id, o.waba_count::int AS waba_count,
       coalesce(o.plan_code, '')::text AS plan_code, coalesce(o.subscription_status, '')::text AS subscription_status,
       o.numbers::int AS numbers, o.messages_30d::int AS messages_30d, coalesce(o.worst_quality, '')::text AS worst_quality
FROM admin_tenant_overview(@ids::uuid[]) o;

-- name: AdminTenantKPIs :one
SELECT (count(*) FILTER (WHERE status = 'active'))::int AS active,
       (count(*) FILTER (WHERE status = 'active' AND created_at > now() - interval '7 days'))::int AS new_this_week,
       (count(*) FILTER (WHERE status = 'suspended'))::int AS suspended
FROM tenants;

-- name: AdminMetaErrorCounts :one
-- Meta API errors in the window ending now and in the window before it.
SELECT (count(*) FILTER (WHERE occurred_at > @since::timestamptz))::int AS current,
       (count(*) FILTER (WHERE occurred_at <= @since::timestamptz))::int AS previous
FROM meta_api_errors
WHERE occurred_at > @previous_since::timestamptz;

-- name: AdminMetaErrorGroups :many
SELECT code, subcode, http_status, coalesce(min(message), '')::text AS message, count(*)::int AS n,
       count(DISTINCT tenant_id)::int AS tenants, max(occurred_at)::timestamptz AS last_at
FROM meta_api_errors
WHERE occurred_at > @since::timestamptz
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id))
GROUP BY code, subcode, http_status
ORDER BY n DESC
LIMIT 50;
