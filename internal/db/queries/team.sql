-- name: ListTeam :many
SELECT u.id, u.name, u.email, u.last_login_at, m.role, m.created_at
FROM memberships m JOIN users u ON u.id = m.user_id
ORDER BY m.created_at;

-- name: GetMemberRole :one
SELECT role FROM memberships WHERE user_id = $1;

-- name: SetMemberRole :exec
UPDATE memberships SET role = $2 WHERE user_id = $1;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE user_id = $1;

-- name: ClearSessionsForTenant :exec
-- A removed member's sessions in this workspace lose it as their selected tenant.
UPDATE sessions SET tenant_id = NULL WHERE user_id = @user_id AND tenant_id = @tenant_id;

-- name: DeleteOpenInvite :exec
DELETE FROM invites WHERE email = $1 AND accepted_at IS NULL;

-- name: InsertInvite :one
INSERT INTO invites (id, tenant_id, email, role, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListOpenInvites :many
SELECT i.id, i.email, i.role, i.expires_at, i.created_at, u.name AS invited_by_name
FROM invites i JOIN users u ON u.id = i.invited_by
WHERE i.accepted_at IS NULL
ORDER BY i.created_at DESC;

-- name: DeleteInvite :execrows
DELETE FROM invites WHERE id = $1 AND accepted_at IS NULL;

-- name: InviteByToken :one
SELECT i.id::uuid AS id, i.tenant_id::uuid AS tenant_id, i.tenant_name::text AS tenant_name, i.email::text AS email,
       i.role::member_role AS role, i.invited_by_name::text AS invited_by_name,
       i.expires_at::timestamptz AS expires_at, (i.accepted_at IS NOT NULL)::boolean AS accepted
FROM invite_by_token(@token_hash::bytea) i;

-- name: MarkInviteAccepted :execrows
UPDATE invites SET accepted_at = now() WHERE id = $1 AND accepted_at IS NULL;

-- name: CreateVerifiedUser :one
INSERT INTO users (id, email, name, password_hash, email_verified_at)
VALUES ($1, $2, $3, $4, now())
RETURNING *;

-- name: UpdateTenantSettings :one
UPDATE tenants SET name = $2, legal_name = $3, timezone = $4, message_retention_days = $5, updated_at = now() WHERE id = $1
RETURNING *;

-- name: SetPassword :exec
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions WHERE user_id = @user_id AND id <> @keep_id;

-- name: UpdateUserName :exec
UPDATE users SET name = $2, updated_at = now() WHERE id = $1;

-- name: RefreshInvite :one
UPDATE invites SET token_hash = @token_hash, expires_at = @expires_at
WHERE id = @id AND accepted_at IS NULL
RETURNING *;

-- name: SetRequireTwoFactor :exec
UPDATE tenants SET require_two_factor = @require_two_factor, updated_at = now() WHERE id = @id;

-- name: TenantRequiresTwoFactor :one
SELECT require_two_factor FROM tenants WHERE id = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;
