-- name: InsertMetaWebhookEvent :one
INSERT INTO meta_webhook_events (payload_hash, received_at, object, field, waba_id, meta_phone_number_id, tenant_id, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (payload_hash) DO NOTHING
RETURNING id;

-- name: GetMetaWebhookEventByHash :one
SELECT id, processed_at FROM meta_webhook_events WHERE payload_hash = $1;

-- name: FinishMetaWebhookEvent :exec
UPDATE meta_webhook_events SET processed_at = now(), error = $2 WHERE id = $1;

-- name: RouteMetaEvent :one
SELECT coalesce(route_meta_event(@phone_number_id::text, @waba_id::text), '00000000-0000-0000-0000-000000000000')::uuid AS tenant_id;

-- name: UpsertContactFromWhatsApp :one
INSERT INTO contacts (id, tenant_id, wa_id, profile_name, name)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, wa_id) DO UPDATE
SET profile_name = coalesce(EXCLUDED.profile_name, contacts.profile_name),
    name = coalesce(contacts.name, EXCLUDED.name),
    updated_at = now()
RETURNING *;

-- name: UpsertConversation :one
INSERT INTO conversations (id, tenant_id, phone_number_id, contact_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (phone_number_id, contact_id) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: InsertWhatsAppMessage :one
-- Inserts a message that came from WhatsApp (inbound, or an echo from the Business app).
-- A replayed wamid inserts nothing.
INSERT INTO messages (id, tenant_id, conversation_id, phone_number_id, contact_id, direction, origin,
                      wamid, type, content, reply_to_wamid, status, meta_timestamp)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (wamid) DO NOTHING
RETURNING id;

-- name: TouchConversationInbound :exec
UPDATE conversations
SET last_inbound_at = greatest(coalesce(last_inbound_at, @at::timestamptz), @at::timestamptz),
    last_message_at = greatest(coalesce(last_message_at, @at::timestamptz), @at::timestamptz),
    last_message_preview = CASE WHEN last_message_at IS NULL OR last_message_at <= @at::timestamptz
                                THEN @preview::text ELSE last_message_preview END,
    unread_count = unread_count + 1,
    status = CASE WHEN status = 'closed' THEN 'open'::conversation_status ELSE status END,
    updated_at = now()
WHERE id = @id;

-- name: TouchConversationOutbound :exec
UPDATE conversations
SET last_message_at = greatest(coalesce(last_message_at, @at::timestamptz), @at::timestamptz),
    last_message_preview = CASE WHEN last_message_at IS NULL OR last_message_at <= @at::timestamptz
                                THEN @preview::text ELSE last_message_preview END,
    updated_at = now()
WHERE id = @id;

-- name: GetMessageByWamid :one
SELECT * FROM messages WHERE wamid = $1;

-- name: AdvanceMessageStatus :execrows
UPDATE messages
SET status = @status, error_code = coalesce(@error_code, error_code), error_title = coalesce(@error_title, error_title),
    pricing_category = coalesce(@pricing_category, pricing_category),
    pricing_billable = coalesce(@pricing_billable, pricing_billable),
    status_updated_at = now()
WHERE id = @id AND message_status_rank(@status) > message_status_rank(status);

-- name: InsertMessageStatusEvent :exec
INSERT INTO message_status_events (tenant_id, message_id, status, error_code, error_title, occurred_at, raw)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: UpdateTemplateStatusByMetaID :many
UPDATE templates
SET status = @status, rejected_reason = @rejected_reason, status_updated_at = now(), updated_at = now()
WHERE meta_template_id = @meta_template_id
RETURNING *;

-- name: UpdatePhoneLimitTierByDisplay :many
UPDATE phone_numbers p
SET messaging_limit_tier = @tier, updated_at = now()
FROM whatsapp_accounts w
WHERE w.id = p.whatsapp_account_id AND w.waba_id = @waba_id
  AND regexp_replace(p.display_phone_number, '[^0-9]', '', 'g') = @display_digits::text
RETURNING p.*;

-- name: RevokeWhatsAppAccount :exec
UPDATE whatsapp_accounts SET status = 'revoked', disconnected_at = now(), updated_at = now() WHERE id = $1;

-- name: SetAccountPhoneNumbersStatus :exec
UPDATE phone_numbers SET status = $2, updated_at = now() WHERE whatsapp_account_id = $1;

-- name: UpdateTemplateQualityByMetaID :many
UPDATE templates SET quality_score = @quality_score, updated_at = now()
WHERE meta_template_id = @meta_template_id
RETURNING *;

-- name: UpdatePhoneNameByDisplay :many
-- A name review result; verified_name changes only when the new name was approved.
UPDATE phone_numbers p
SET name_status = @name_status, verified_name = coalesce(@verified_name, p.verified_name), updated_at = now()
FROM whatsapp_accounts w
WHERE w.id = p.whatsapp_account_id AND w.waba_id = @waba_id
  AND regexp_replace(p.display_phone_number, '[^0-9]', '', 'g') = @display_digits::text
RETURNING p.*;
