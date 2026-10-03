-- name: CreateUser :one
INSERT INTO users (id, email, name, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now() WHERE id = $1;

-- name: UpdateLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: CreateTenant :one
INSERT INTO tenants (id, name, slug)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: CreateMembership :exec
INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3);

-- name: UserMemberships :many
SELECT m.tenant_id::uuid AS tenant_id, m.tenant_name::text AS tenant_name, m.tenant_slug::text AS tenant_slug,
       m.tenant_status::tenant_status AS tenant_status, m.role::member_role AS role
FROM user_memberships(@user_id::uuid) m;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, tenant_id, token_hash, ip, user_agent, expires_at, persistent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = $1 AND expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1;

-- name: SetSessionTenant :exec
UPDATE sessions SET tenant_id = $2 WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (tenant_id, actor_type, actor_id, action, target_type, target_id, ip, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
