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

-- name: UpdatePhoneNumberFromMeta :one
-- Refreshes a number with what Meta reports now. Rows of other workspaces are not visible.
UPDATE phone_numbers
SET display_phone_number = @display_phone_number, verified_name = sqlc.narg(verified_name),
    name_status = sqlc.narg(name_status), quality_rating = @quality_rating,
    messaging_limit_tier = sqlc.narg(messaging_limit_tier),
    code_verification_status = sqlc.narg(code_verification_status), last_synced_at = now(), updated_at = now()
WHERE phone_number_id = @phone_number_id
RETURNING *;

-- name: NumbersUsageToday :many
-- Distinct customers each number sent a template in the last 24 hours: what Meta's messaging
-- limit counts.
SELECT phone_number_id, count(DISTINCT contact_id)::int AS used FROM messages
WHERE direction = 'outbound' AND type = 'template' AND status <> 'failed'
  AND created_at > now() - interval '24 hours'
GROUP BY phone_number_id;
