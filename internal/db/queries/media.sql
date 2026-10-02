-- name: InsertMedia :one
INSERT INTO media (id, tenant_id, phone_number_id, storage_key, mime_type, size_bytes, sha256, filename, meta_media_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetMedia :one
SELECT * FROM media WHERE id = $1;

-- name: MarkMediaUploaded :exec
-- Records the ID Meta gave an upload from a phone number; Meta keeps it for 30 days.
UPDATE media SET phone_number_id = $2, meta_media_id = $3, meta_uploaded_at = $4 WHERE id = $1;

-- name: SetMessageMedia :exec
UPDATE messages SET media_id = $2 WHERE id = $1;

-- name: GetMessageForMediaDownload :one
SELECT sqlc.embed(m), p.whatsapp_account_id
FROM messages m JOIN phone_numbers p ON p.id = m.phone_number_id
WHERE m.id = $1;
