-- name: InsertWebhookEndpoint :one
INSERT INTO webhook_endpoints (id, tenant_id, url, description, secret_ciphertext, event_types, phone_number_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWebhookEndpoints :many
SELECT * FROM webhook_endpoints ORDER BY created_at DESC;

-- name: GetWebhookEndpoint :one
SELECT * FROM webhook_endpoints WHERE id = $1;

-- name: SetWebhookEndpointEnabled :one
UPDATE webhook_endpoints
SET is_enabled = @is_enabled, disabled_reason = sqlc.narg(disabled_reason)
WHERE id = @id
RETURNING *;

-- name: SkipOpenDeliveries :execrows
-- Closes an endpoint's deliveries still waiting to be sent, without counting an attempt.
UPDATE webhook_deliveries
SET status = 'dead', next_attempt_at = now(),
    last_error = CASE WHEN last_error IS NULL THEN @note::text ELSE @note::text || ' Last error: ' || last_error END
WHERE endpoint_id = @endpoint_id AND status IN ('pending', 'retrying');

-- name: SkipDelivery :exec
-- Closes one delivery that will not be sent, without counting an attempt.
UPDATE webhook_deliveries
SET status = 'dead', next_attempt_at = now(),
    last_error = CASE WHEN last_error IS NULL THEN @note::text ELSE @note::text || ' Last error: ' || last_error END
WHERE id = @id AND status IN ('pending', 'retrying');

-- name: DeleteWebhookEndpoint :execrows
DELETE FROM webhook_endpoints WHERE id = $1;

-- name: InsertDeliveriesForEvent :many
-- Creates one delivery per enabled endpoint that subscribes to the event (and its number).
INSERT INTO webhook_deliveries (tenant_id, endpoint_id, event_id, event_type, payload)
SELECT e.tenant_id, e.id, @event_id, @event_type::text, @payload
FROM webhook_endpoints e
WHERE e.is_enabled AND @event_type::text = ANY (e.event_types)
  AND (e.phone_number_id IS NULL OR e.phone_number_id = sqlc.narg(phone_number_id))
RETURNING id;

-- name: GetDeliveryForSend :one
SELECT sqlc.embed(d), sqlc.embed(e)
FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id = d.endpoint_id
WHERE d.id = $1;

-- name: RecordDeliveryAttempt :exec
UPDATE webhook_deliveries
SET status = @status, attempt_count = attempt_count + 1, last_response_code = @last_response_code,
    last_error = @last_error, last_attempt_at = now(), next_attempt_at = @next_attempt_at
WHERE id = @id;

-- name: ListDeliveries :many
SELECT * FROM webhook_deliveries
WHERE endpoint_id = @endpoint_id
  AND (sqlc.narg(status)::delivery_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: RequeueDelivery :execrows
UPDATE webhook_deliveries SET status = 'pending', next_attempt_at = now()
WHERE id = @id AND endpoint_id = @endpoint_id;

-- name: DeliveryStatsSince :many
-- Deliveries created since a time, by endpoint and status.
SELECT endpoint_id, status, count(*)::int AS n
FROM webhook_deliveries
WHERE created_at >= @since
GROUP BY endpoint_id, status;

-- name: GetDelivery :one
SELECT * FROM webhook_deliveries WHERE id = @id AND endpoint_id = @endpoint_id;

-- name: SetWebhookSecret :exec
UPDATE webhook_endpoints SET secret_ciphertext = @secret_ciphertext WHERE id = @id;

-- name: DeliverySeries :many
-- Deliveries per endpoint and bucket (an hour, or a day in the workspace's time zone) since a time:
-- by status, how many needed more than one attempt, and the attempts made.
SELECT endpoint_id,
       (CASE WHEN @hourly::bool THEN date_trunc('hour', created_at)
             ELSE date_trunc('day', created_at AT TIME ZONE @tz::text) AT TIME ZONE @tz::text END)::timestamptz AS bucket,
       count(*)::int AS deliveries,
       count(*) FILTER (WHERE status = 'succeeded')::int AS succeeded,
       count(*) FILTER (WHERE status = 'dead')::int AS failed,
       count(*) FILTER (WHERE status = 'retrying')::int AS retrying,
       count(*) FILTER (WHERE status = 'pending')::int AS pending,
       count(*) FILTER (WHERE attempt_count > 1)::int AS retried,
       coalesce(sum(attempt_count), 0)::int AS attempts
FROM webhook_deliveries
WHERE created_at >= @since
GROUP BY 1, 2
ORDER BY 2;
