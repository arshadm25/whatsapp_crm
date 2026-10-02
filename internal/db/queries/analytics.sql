-- name: UsageForPeriod :many
-- Message counts for one day of a tenant, by number, pricing category, recipient calling-code
-- prefix and origin. sent, delivered and read are cumulative.
SELECT m.phone_number_id,
       coalesce(m.pricing_category, CASE WHEN m.direction = 'outbound' THEN t.category::text END, 'service')::text AS category,
       left(c.wa_id, 3)::text AS prefix,
       m.origin,
       (count(*) FILTER (WHERE m.direction = 'outbound' AND m.status IN ('sent', 'delivered', 'read')))::int AS sent,
       (count(*) FILTER (WHERE m.direction = 'outbound' AND m.status IN ('delivered', 'read')))::int AS delivered,
       (count(*) FILTER (WHERE m.direction = 'outbound' AND m.status = 'read'))::int AS read,
       (count(*) FILTER (WHERE m.direction = 'outbound' AND m.status = 'failed'))::int AS failed,
       (count(*) FILTER (WHERE m.direction = 'inbound'))::int AS received,
       (count(*) FILTER (WHERE m.direction = 'outbound' AND m.pricing_billable))::int AS billable
FROM messages m
JOIN contacts c ON c.id = m.contact_id
LEFT JOIN templates t ON t.id = m.template_id
WHERE m.created_at >= @start AND m.created_at < @end_at
GROUP BY 1, 2, 3, 4;

-- name: DeleteUsageDay :exec
DELETE FROM usage_daily WHERE day = $1;

-- name: InsertUsage :exec
INSERT INTO usage_daily (tenant_id, phone_number_id, day, pricing_category, recipient_country, origin,
                         sent, delivered, read, failed, received, billable, est_meta_cost_minor)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: UsageRange :many
SELECT * FROM usage_daily
WHERE day BETWEEN @from_day AND @to_day
  AND (sqlc.narg(phone_number_id)::uuid IS NULL OR phone_number_id = sqlc.narg(phone_number_id))
ORDER BY day;

-- name: ListActiveTenants :many
SELECT id, timezone FROM tenants WHERE status = 'active' ORDER BY id;

-- name: FirstResponses :many
-- How long each customer waited for a first reply. A customer's turn starts with an inbound
-- message that follows an outbound one (or opens the conversation); the reply is the next
-- outbound message, by a team member (sent_by_user_id) or by a bot or the API (NULL).
WITH turns AS (
    SELECT m.conversation_id, m.created_at, m.direction,
           lag(m.direction) OVER (PARTITION BY m.conversation_id ORDER BY m.created_at, m.id) AS prev_dir
    FROM messages m
    WHERE m.created_at >= @start AND m.created_at < @end_at
)
SELECT r.sent_by_user_id, extract(epoch FROM r.created_at - t.created_at)::float8 AS seconds
FROM turns t
CROSS JOIN LATERAL (
    SELECT o.created_at, o.sent_by_user_id FROM messages o
    WHERE o.conversation_id = t.conversation_id AND o.direction = 'outbound' AND o.created_at > t.created_at
    ORDER BY o.created_at LIMIT 1
) r
WHERE t.direction = 'inbound' AND (t.prev_dir IS NULL OR t.prev_dir = 'outbound');

-- name: AgentChats :many
-- Conversations each team member replied in.
SELECT sent_by_user_id::uuid AS user_id, count(DISTINCT conversation_id)::int AS chats, count(*)::int AS messages
FROM messages
WHERE direction = 'outbound' AND sent_by_user_id IS NOT NULL AND created_at >= @start AND created_at < @end_at
GROUP BY sent_by_user_id;

-- name: AgentAssignments :many
-- Conversations assigned to each team member that were active in the period, and how many are closed.
SELECT assignee_user_id::uuid AS user_id, count(*)::int AS assigned,
       (count(*) FILTER (WHERE status = 'closed'))::int AS resolved
FROM conversations
WHERE assignee_user_id IS NOT NULL AND coalesce(last_message_at, created_at) >= @start::timestamptz
  AND created_at < @end_at
GROUP BY assignee_user_id;

-- name: ConversationsByNumber :many
-- Conversations with at least one message in the period, per number.
SELECT phone_number_id, count(DISTINCT conversation_id)::int AS conversations
FROM messages
WHERE created_at >= @start AND created_at < @end_at
  AND (sqlc.narg(phone_number_id)::uuid IS NULL OR phone_number_id = sqlc.narg(phone_number_id))
GROUP BY phone_number_id;
