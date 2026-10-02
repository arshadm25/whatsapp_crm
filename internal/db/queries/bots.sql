-- name: InsertBot :one
INSERT INTO bots (id, tenant_id, name, phone_number_id, flow, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetBot :one
SELECT * FROM bots WHERE id = $1;

-- name: ListBots :many
SELECT * FROM bots ORDER BY created_at, id;

-- name: UpdateBot :one
UPDATE bots
SET name = @name, flow = @flow, phone_number_id = @phone_number_id, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetBotStatus :one
UPDATE bots SET status = @status, updated_at = now() WHERE id = @id RETURNING *;

-- name: DeleteBot :execrows
DELETE FROM bots WHERE id = $1;

-- name: ListActiveBotsForNumber :many
SELECT * FROM bots
WHERE status = 'active' AND (phone_number_id IS NULL OR phone_number_id = @phone_number_id)
ORDER BY (phone_number_id IS NULL), created_at, id;

-- name: GetActiveBotSession :one
SELECT * FROM bot_sessions WHERE conversation_id = $1 AND status = 'active' FOR UPDATE;

-- name: GetBotSession :one
SELECT * FROM bot_sessions WHERE id = $1;

-- name: InsertBotSession :one
INSERT INTO bot_sessions (id, tenant_id, bot_id, conversation_id, contact_id, vars)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: SaveBotSession :exec
UPDATE bot_sessions
SET node_id = @node_id, vars = @vars, status = @status, end_reason = @end_reason, updated_at = now(),
    ended_at = CASE WHEN @status::text = 'active' THEN NULL ELSE now() END
WHERE id = @id;

-- name: ListBotSessions :many
SELECT * FROM bot_sessions WHERE bot_id = @bot_id ORDER BY started_at DESC, id LIMIT @lim;

-- name: StopBotSessionsForBot :exec
UPDATE bot_sessions SET status = 'stopped', end_reason = 'bot_paused', ended_at = now(), updated_at = now()
WHERE bot_id = $1 AND status = 'active';

-- name: GetBotConversation :one
SELECT sqlc.embed(cv), sqlc.embed(ct)
FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
WHERE cv.id = $1
FOR UPDATE OF cv;

-- name: CountInboundMessages :one
SELECT count(*) FROM messages WHERE conversation_id = $1 AND direction = 'inbound';

-- name: HasHumanReplySince :one
SELECT EXISTS (
    SELECT 1 FROM messages
    WHERE conversation_id = @conversation_id AND direction = 'outbound' AND origin = 'dashboard' AND created_at >= @since::timestamptz
);

-- name: InsertBotMessage :one
INSERT INTO messages (id, tenant_id, conversation_id, phone_number_id, contact_id, direction, origin,
                      type, content, template_id, status, bot_id)
VALUES ($1, $2, $3, $4, $5, 'outbound', 'bot', $6, $7, $8, 'queued', $9)
RETURNING *;

-- name: GetInboundMessage :one
SELECT * FROM messages WHERE id = $1 AND direction = 'inbound';

-- name: BotsInUse :one
-- Whether an inbound message in this conversation could involve a bot.
SELECT EXISTS (SELECT 1 FROM bots WHERE status = 'active')
    OR EXISTS (SELECT 1 FROM bot_sessions WHERE conversation_id = $1 AND status = 'active');
