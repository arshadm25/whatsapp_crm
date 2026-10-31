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

-- name: InsertAPIRequestLog :exec
INSERT INTO api_request_log (tenant_id, api_key_id, method, path, route, status, duration_ms, request_id)
VALUES (@tenant_id, @api_key_id, @method, @path, @route, @status, @duration_ms, @request_id);

-- name: APIUsageSeries :many
-- Calls and errors per key and bucket (an hour, or a day in the workspace's time zone) since a time.
SELECT api_key_id,
       (CASE WHEN @hourly::bool THEN hour
             ELSE date_trunc('day', hour AT TIME ZONE @tz::text) AT TIME ZONE @tz::text END)::timestamptz AS bucket,
       sum(calls)::int AS calls, sum(errors)::int AS errors
FROM api_usage_hourly
WHERE hour >= date_trunc('hour', @since::timestamptz)
GROUP BY 1, 2
ORDER BY 2;

-- name: ListAPIRequestLog :many
-- Newest first, before an id for paging. status_class: 2, 3, 4 or 5 for that hundred, 0 for any.
SELECT l.id, l.api_key_id, k.name AS key_name, k.prefix AS key_prefix, l.method, l.path, l.route,
       l.status, l.duration_ms, l.request_id, l.created_at
FROM api_request_log l JOIN api_keys k ON k.id = l.api_key_id
WHERE (sqlc.narg(before)::bigint IS NULL OR l.id < sqlc.narg(before)::bigint)
  AND (sqlc.narg(api_key_id)::uuid IS NULL OR l.api_key_id = sqlc.narg(api_key_id)::uuid)
  AND (@status_class::int = 0 OR l.status / 100 = @status_class::int)
  AND (@errors_only::bool = false OR l.status >= 400)
  AND (@method::text = '' OR l.method = @method::text)
  AND (@path::text = '' OR l.path ILIKE '%' || @path::text || '%')
ORDER BY l.id DESC
LIMIT @lim;

-- name: APIRequestLogTotals :one
SELECT count(*)::int AS calls,
       count(*) FILTER (WHERE status >= 400)::int AS errors,
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms), 0)::float8 AS p50_ms,
       coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)::float8 AS p95_ms,
       coalesce(max(duration_ms), 0)::int AS max_ms
FROM api_request_log
WHERE created_at >= @since;

-- name: APIRequestLogByStatus :many
SELECT status, count(*)::int AS n
FROM api_request_log
WHERE created_at >= @since
GROUP BY status
ORDER BY n DESC;

-- name: APIRequestLogSeries :many
-- Requests, errors and 95th percentile latency per bucket (an hour, or a day in the workspace's time zone).
SELECT (CASE WHEN @hourly::bool THEN date_trunc('hour', created_at)
             ELSE date_trunc('day', created_at AT TIME ZONE @tz::text) AT TIME ZONE @tz::text END)::timestamptz AS bucket,
       count(*)::int AS calls,
       count(*) FILTER (WHERE status >= 400)::int AS errors,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)::float8 AS p95_ms
FROM api_request_log
WHERE created_at >= @since
GROUP BY 1
ORDER BY 1;

-- name: APIRequestLogByRoute :many
SELECT method, (CASE WHEN route = '' THEN path ELSE route END)::text AS route,
       count(*)::int AS calls,
       count(*) FILTER (WHERE status >= 400)::int AS errors,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)::float8 AS p95_ms
FROM api_request_log
WHERE created_at >= @since
GROUP BY 1, 2
ORDER BY calls DESC
LIMIT 20;

-- name: PurgeAPIRequestLog :one
SELECT purge_api_request_log(@cutoff::timestamptz, @batch::int)::bigint AS n;
