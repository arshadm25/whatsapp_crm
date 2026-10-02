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

-- name: CountAPICall :exec
INSERT INTO api_usage_hourly (tenant_id, api_key_id, hour, calls, errors)
VALUES (@tenant_id, @api_key_id, date_trunc('hour', now()), 1, CASE WHEN @failed::bool THEN 1 ELSE 0 END)
ON CONFLICT (api_key_id, hour) DO UPDATE
SET calls = api_usage_hourly.calls + 1, errors = api_usage_hourly.errors + EXCLUDED.errors;

-- name: APIUsageSince :many
-- Calls and errors per key since a time (whole hours).
SELECT api_key_id, sum(calls)::int AS calls, sum(errors)::int AS errors
FROM api_usage_hourly
WHERE hour >= date_trunc('hour', @since::timestamptz)
GROUP BY api_key_id;
