-- name: ListRetentionTenants :many
SELECT id, message_retention_days FROM tenants
WHERE status <> 'closed' AND message_retention_days IS NOT NULL
ORDER BY id;

-- name: PurgeOldMessages :execrows
-- Deletes at most @batch of the oldest messages from before @cutoff.
DELETE FROM messages
WHERE id IN (SELECT m.id FROM messages m WHERE m.created_at < @cutoff ORDER BY m.created_at LIMIT @batch);

-- name: ListExpiredMedia :many
SELECT id, storage_key FROM media WHERE created_at < @cutoff ORDER BY created_at LIMIT @batch;

-- name: DeleteMedia :exec
DELETE FROM media WHERE id = $1;

-- name: ClearOldConversationPreviews :execrows
-- A conversation whose latest message was purged no longer shows its text in the inbox list.
UPDATE conversations SET last_message_preview = NULL
WHERE last_message_at < @cutoff AND last_message_preview IS NOT NULL;
