-- name: GetSendingNumber :one
SELECT sqlc.embed(p), sqlc.embed(w)
FROM phone_numbers p JOIN whatsapp_accounts w ON w.id = p.whatsapp_account_id
WHERE p.id = $1;

-- name: ClaimIdempotencyKey :one
-- Returns the key when this request is the first to use it; no row means the key exists.
INSERT INTO idempotency_records (tenant_id, key, request_hash)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, key) DO NOTHING
RETURNING key;

-- name: GetIdempotencyRecordForUpdate :one
SELECT * FROM idempotency_records WHERE tenant_id = $1 AND key = $2 FOR UPDATE;

-- name: RestartIdempotencyRecord :exec
-- Reuses a key whose 24 hours have passed.
UPDATE idempotency_records
SET request_hash = $3, response_status = NULL, response_body = NULL, created_at = now()
WHERE tenant_id = $1 AND key = $2;

-- name: SaveIdempotencyResponse :exec
UPDATE idempotency_records SET response_status = $3, response_body = $4 WHERE tenant_id = $1 AND key = $2;

-- name: ClearIdempotencyKey :exec
-- Frees a key held by an earlier message whose 24 hours have passed.
UPDATE messages SET idempotency_key = NULL WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: GetMessageByID :one
SELECT * FROM messages WHERE id = $1;

-- name: InsertOutboundMessage :one
INSERT INTO messages (id, tenant_id, conversation_id, phone_number_id, contact_id, direction, origin,
                      type, content, template_id, reply_to_wamid, status, sent_by_user_id, idempotency_key, media_id)
VALUES ($1, $2, $3, $4, $5, 'outbound', $6, $7, $8, $9, $10, 'queued', $11, $12, $13)
RETURNING *;

-- name: GetMessageView :one
SELECT sqlc.embed(m), c.wa_id AS contact_wa_id, coalesce(c.name, c.profile_name) AS contact_name
FROM messages m JOIN contacts c ON c.id = m.contact_id
WHERE m.id = $1;

-- name: GetMessageForSend :one
SELECT sqlc.embed(m), c.wa_id AS contact_wa_id, p.phone_number_id AS meta_phone_number_id,
       p.whatsapp_account_id, p.status AS phone_status
FROM messages m
JOIN contacts c ON c.id = m.contact_id
JOIN phone_numbers p ON p.id = m.phone_number_id
WHERE m.id = $1;

-- name: MarkMessageSent :execrows
UPDATE messages SET wamid = $2, status = 'sent', status_updated_at = now()
WHERE id = $1 AND status = 'queued';

-- name: MarkMessageFailed :execrows
UPDATE messages SET status = 'failed', error_code = $2, error_title = $3, status_updated_at = now()
WHERE id = $1 AND status = 'queued';

-- name: HasUnsentMessageTo :one
-- True while an outbound message to this customer is still waiting for Meta's message ID.
SELECT EXISTS (
    SELECT 1 FROM messages m JOIN contacts c ON c.id = m.contact_id
    WHERE m.phone_number_id = @phone_number_id AND c.wa_id = @wa_id
      AND m.direction = 'outbound' AND m.status = 'queued' AND m.wamid IS NULL
)::boolean;

-- name: ResetConversationUnread :exec
UPDATE conversations SET unread_count = 0, updated_at = now() WHERE id = $1;
