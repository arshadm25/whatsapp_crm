-- name: InsertFlow :one
INSERT INTO flows (id, tenant_id, whatsapp_account_id, meta_flow_id, name, categories, status, flow_json,
                   validation_errors, preview_url, preview_expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetFlow :one
SELECT * FROM flows WHERE id = $1;

-- name: GetFlowByMetaID :one
SELECT * FROM flows WHERE meta_flow_id = $1;

-- name: ListFlows :many
SELECT * FROM flows ORDER BY created_at DESC, id DESC;

-- name: UpdateFlowContent :one
UPDATE flows
SET name = @name, categories = @categories, flow_json = @flow_json, validation_errors = @validation_errors,
    preview_url = @preview_url, preview_expires_at = @preview_expires_at, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: UpdateFlowStatus :one
UPDATE flows
SET status = @status,
    published_at = CASE WHEN @status::text = 'published' THEN coalesce(published_at, now()) ELSE published_at END,
    validation_errors = coalesce(sqlc.narg(validation_errors), validation_errors),
    preview_url = coalesce(sqlc.narg(preview_url), preview_url),
    preview_expires_at = coalesce(sqlc.narg(preview_expires_at), preview_expires_at),
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteFlow :execrows
DELETE FROM flows WHERE id = $1;

-- name: InsertFlowSubmission :one
INSERT INTO flow_submissions (id, tenant_id, flow_id, meta_flow_id, contact_id, conversation_id, message_id, flow_token, response)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (message_id) DO NOTHING
RETURNING *;

-- name: ListFlowSubmissions :many
SELECT sqlc.embed(s), c.wa_id AS contact_wa_id, coalesce(c.name, c.profile_name) AS contact_name, f.name AS flow_name
FROM flow_submissions s
JOIN contacts c ON c.id = s.contact_id
LEFT JOIN flows f ON f.id = s.flow_id
WHERE (sqlc.narg(flow_id)::uuid IS NULL OR s.flow_id = sqlc.narg(flow_id))
  AND (sqlc.narg(contact_id)::uuid IS NULL OR s.contact_id = sqlc.narg(contact_id))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (s.created_at, s.id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY s.created_at DESC, s.id DESC
LIMIT @lim;

-- name: GetFlowSubmissionView :one
SELECT sqlc.embed(s), c.wa_id AS contact_wa_id, coalesce(c.name, c.profile_name) AS contact_name, f.name AS flow_name
FROM flow_submissions s
JOIN contacts c ON c.id = s.contact_id
LEFT JOIN flows f ON f.id = s.flow_id
WHERE s.id = $1;
