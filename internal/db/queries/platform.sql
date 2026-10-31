-- name: GetPlatformSettings :one
SELECT * FROM platform_settings WHERE id;

-- name: UpsertPlatformSettings :one
INSERT INTO platform_settings (id, gst_rate_bp, meta_markup_bp, ai_model, updated_by, updated_at)
VALUES (true, sqlc.narg('gst_rate_bp'), sqlc.narg('meta_markup_bp'), sqlc.narg('ai_model'), sqlc.narg('updated_by'), now())
ON CONFLICT (id) DO UPDATE SET gst_rate_bp = EXCLUDED.gst_rate_bp, meta_markup_bp = EXCLUDED.meta_markup_bp,
    ai_model = EXCLUDED.ai_model, updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: SetTenantMetaMarkup :one
UPDATE tenants SET meta_markup_bp = sqlc.narg('markup_bp'), updated_at = now()
WHERE id = @id RETURNING *;
