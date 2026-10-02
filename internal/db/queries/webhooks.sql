-- name: InsertWebhookEndpoint :one
INSERT INTO webhook_endpoints (id, tenant_id, url, description, secret_ciphertext, event_types, phone_number_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWebhookEndpoints :many
SELECT * FROM webhook_endpoints ORDER BY created_at DESC;

-- name: GetWebhookEndpoint :one
SELECT * FROM webhook_endpoints WHERE id = $1;

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
