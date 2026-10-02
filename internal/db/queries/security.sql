-- name: GetSessionAuth :one
-- The session with what the middleware needs to know about its user.
SELECT sqlc.embed(s), (u.totp_secret_enc IS NOT NULL)::boolean AS totp_enabled, u.is_platform_admin
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: SetTOTPSecret :exec
UPDATE users SET totp_secret_enc = $2, updated_at = now() WHERE id = $1;

-- name: SetSessionMFAPassed :exec
UPDATE sessions SET mfa_passed = true WHERE id = $1;

-- name: SetPlatformAdmin :execrows
UPDATE users SET is_platform_admin = $2, updated_at = now() WHERE email = $1;

-- name: CountRecentAudit :one
-- Recent entries of one action by one user, for throttling repeated failures.
SELECT count(*) FROM audit_log WHERE actor_id = $1 AND action = $2 AND occurred_at > $3;
