-- name: ListWhatsAppAccounts :many
SELECT * FROM whatsapp_accounts ORDER BY created_at;

-- name: GetWhatsAppAccount :one
SELECT * FROM whatsapp_accounts WHERE id = $1;

-- name: ListTemplates :many
SELECT * FROM templates
WHERE (sqlc.narg(whatsapp_account_id)::uuid IS NULL OR whatsapp_account_id = sqlc.narg(whatsapp_account_id))
  AND (sqlc.narg(status)::template_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(category)::template_category IS NULL OR category = sqlc.narg(category))
  AND (sqlc.narg(name)::text IS NULL OR name = sqlc.narg(name))
  AND (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before))
  AND status <> 'deleted'
ORDER BY id DESC
LIMIT @lim;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = $1;

-- name: GetTemplateByName :one
SELECT * FROM templates WHERE whatsapp_account_id = $1 AND name = $2 AND language = $3;

-- name: UpsertTemplateFromMeta :one
INSERT INTO templates (id, tenant_id, whatsapp_account_id, meta_template_id, name, language, category, status,
                       rejected_reason, quality_score, parameter_format, components, created_by, submitted_at,
                       status_updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
ON CONFLICT (whatsapp_account_id, name, language) DO UPDATE
SET meta_template_id = EXCLUDED.meta_template_id,
    category = EXCLUDED.category,
    status = EXCLUDED.status,
    rejected_reason = EXCLUDED.rejected_reason,
    quality_score = EXCLUDED.quality_score,
    parameter_format = EXCLUDED.parameter_format,
    components = EXCLUDED.components,
    created_by = coalesce(templates.created_by, EXCLUDED.created_by),
    submitted_at = coalesce(EXCLUDED.submitted_at, templates.submitted_at),
    status_updated_at = CASE WHEN templates.status <> EXCLUDED.status THEN now() ELSE templates.status_updated_at END,
    updated_at = now()
RETURNING *;

-- name: DeleteTemplatesMissingFromMeta :execrows
DELETE FROM templates
WHERE whatsapp_account_id = @whatsapp_account_id AND meta_template_id IS NOT NULL
  AND NOT (meta_template_id = ANY(@keep::text[]));

-- name: UpdateTemplateAfterEdit :one
UPDATE templates
SET category = $2, components = $3, status = 'pending', rejected_reason = NULL,
    submitted_at = now(), status_updated_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTemplatesByName :execrows
DELETE FROM templates WHERE whatsapp_account_id = $1 AND name = $2;
