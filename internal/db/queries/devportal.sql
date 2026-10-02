-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: ListAPIKeys :many
SELECT * FROM api_keys ORDER BY revoked_at IS NOT NULL, created_at DESC;

-- name: InsertAPIKey :one
INSERT INTO api_keys (id, tenant_id, name, prefix, key_hash, mode, phone_number_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;
