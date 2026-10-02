-- name: ListPhoneNumbers :many
SELECT sqlc.embed(p), w.waba_id
FROM phone_numbers p JOIN whatsapp_accounts w ON w.id = p.whatsapp_account_id
ORDER BY p.created_at;

-- name: GetPhoneNumber :one
SELECT sqlc.embed(p), w.waba_id
FROM phone_numbers p JOIN whatsapp_accounts w ON w.id = p.whatsapp_account_id
WHERE p.id = $1;

-- name: DisconnectPhoneNumber :exec
-- The number must register with Meta again when it is reconnected.
UPDATE phone_numbers SET status = 'disconnected', registered_at = NULL, updated_at = now() WHERE id = $1;
