-- name: CreateOnboardingSession :one
INSERT INTO onboarding_sessions (id, tenant_id, user_id, flow)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetOnboardingSession :one
SELECT * FROM onboarding_sessions WHERE id = $1;

-- name: GetOnboardingSessionForUpdate :one
SELECT * FROM onboarding_sessions WHERE id = $1 FOR UPDATE;

-- name: ListOnboardingSessions :many
SELECT * FROM onboarding_sessions ORDER BY created_at DESC LIMIT $1;

-- name: SetOnboardingCode :exec
UPDATE onboarding_sessions
SET step = 'code_received', waba_id = $2, phone_number_id = $3, business_id = $4,
    error_code = NULL, error_message = NULL, updated_at = now()
WHERE id = $1;

-- name: SetOnboardingStep :exec
UPDATE onboarding_sessions
SET step = $2, error_code = NULL, error_message = NULL, updated_at = now()
WHERE id = $1;

-- name: SetOnboardingPhoneNumberID :exec
UPDATE onboarding_sessions SET phone_number_id = $2, updated_at = now() WHERE id = $1;

-- name: FailOnboardingSession :exec
UPDATE onboarding_sessions
SET error_code = $2, error_message = $3, attempts = attempts + 1, updated_at = now()
WHERE id = $1;

-- name: CancelOnboardingSession :exec
UPDATE onboarding_sessions
SET step = 'cancelled', error_code = $2, error_message = $3, updated_at = now()
WHERE id = $1 AND step IN ('started', 'code_received');

-- name: WabaOwner :one
SELECT coalesce(waba_owner(@waba_id::text), '00000000-0000-0000-0000-000000000000')::uuid AS tenant_id;

-- name: PhoneNumberOwner :one
SELECT coalesce(phone_number_owner(@phone_number_id::text), '00000000-0000-0000-0000-000000000000')::uuid AS tenant_id;

-- name: UpsertWhatsAppAccount :one
INSERT INTO whatsapp_accounts (id, tenant_id, waba_id, business_id, onboarding_flow)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (waba_id) DO UPDATE
SET business_id = EXCLUDED.business_id, onboarding_flow = EXCLUDED.onboarding_flow,
    status = 'pending', disconnected_at = NULL, updated_at = now()
RETURNING *;

-- name: GetWhatsAppAccountByWabaID :one
SELECT * FROM whatsapp_accounts WHERE waba_id = $1;

-- name: UpdateWhatsAppAccountDetails :exec
UPDATE whatsapp_accounts SET name = $2, currency = $3, timezone_id = $4, updated_at = now() WHERE id = $1;

-- name: MarkWebhooksSubscribed :exec
UPDATE whatsapp_accounts SET webhooks_subscribed_at = now(), updated_at = now() WHERE id = $1;

-- name: MarkWhatsAppAccountConnected :exec
UPDATE whatsapp_accounts SET status = 'connected', connected_at = now(), updated_at = now() WHERE id = $1;

-- name: MarkWhatsAppAccountError :exec
UPDATE whatsapp_accounts SET status = 'error', updated_at = now() WHERE id = $1 AND status <> 'connected';

-- name: DeactivateCredentials :exec
UPDATE meta_credentials SET is_active = false, revoked_at = now()
WHERE whatsapp_account_id = $1 AND is_active;

-- name: InsertCredential :exec
INSERT INTO meta_credentials (id, tenant_id, whatsapp_account_id, token_ciphertext, data_key_ciphertext, master_key_version, scopes)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetActiveCredential :one
SELECT * FROM meta_credentials WHERE whatsapp_account_id = $1 AND is_active;

-- name: TouchCredential :exec
UPDATE meta_credentials SET last_used_at = now() WHERE id = $1;

-- name: UpsertPhoneNumber :one
INSERT INTO phone_numbers (id, tenant_id, whatsapp_account_id, phone_number_id, display_phone_number,
                           verified_name, name_status, quality_rating, messaging_limit_tier,
                           code_verification_status, is_coexistence, last_synced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (phone_number_id) DO UPDATE
SET display_phone_number = EXCLUDED.display_phone_number, verified_name = EXCLUDED.verified_name,
    name_status = EXCLUDED.name_status, quality_rating = EXCLUDED.quality_rating,
    messaging_limit_tier = EXCLUDED.messaging_limit_tier,
    code_verification_status = EXCLUDED.code_verification_status,
    is_coexistence = EXCLUDED.is_coexistence, last_synced_at = now(), updated_at = now()
RETURNING *;

-- name: GetPhoneNumberByMetaID :one
SELECT * FROM phone_numbers WHERE phone_number_id = $1;

-- name: MarkPhoneRegistered :exec
UPDATE phone_numbers SET registered_at = now(), two_step_pin_enc = $2, updated_at = now() WHERE id = $1;

-- name: SetPhoneNumberStatus :exec
UPDATE phone_numbers SET status = $2, updated_at = now() WHERE id = $1;

-- name: InsertMetaAPIError :exec
INSERT INTO meta_api_errors (tenant_id, method, path, http_status, code, subcode, message, fbtrace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetPhoneTwoStepPin :exec
UPDATE phone_numbers SET two_step_pin_enc = $2, updated_at = now() WHERE id = $1;
