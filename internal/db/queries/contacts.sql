-- name: ListContacts :many
SELECT c.* FROM contacts c
WHERE (sqlc.narg(opt_in_status)::opt_in_status IS NULL OR c.opt_in_status = sqlc.narg(opt_in_status))
  AND (sqlc.narg(tag)::text IS NULL OR EXISTS (
        SELECT 1 FROM contact_tags ct JOIN tags t ON t.id = ct.tag_id
        WHERE ct.contact_id = c.id AND t.name = sqlc.narg(tag)::citext))
  AND (sqlc.narg(search)::text IS NULL OR c.wa_id LIKE '%' || sqlc.narg(search) || '%'
       OR c.name ILIKE '%' || sqlc.narg(search) || '%' OR c.profile_name ILIKE '%' || sqlc.narg(search) || '%')
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (c.created_at, c.id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY c.created_at DESC, c.id DESC
LIMIT @lim;

-- name: GetContact :one
SELECT * FROM contacts WHERE id = $1;

-- name: UpsertContact :one
-- Creates a contact or updates the given fields of the one with this number. custom_fields are
-- merged; a null value removes a field.
INSERT INTO contacts (id, tenant_id, wa_id, name, language, custom_fields)
VALUES (@id, @tenant_id, @wa_id, sqlc.narg(name), sqlc.narg(language), jsonb_strip_nulls(coalesce(sqlc.narg(custom_fields)::jsonb, '{}')))
ON CONFLICT (tenant_id, wa_id) DO UPDATE
SET name = coalesce(EXCLUDED.name, contacts.name),
    language = coalesce(EXCLUDED.language, contacts.language),
    custom_fields = jsonb_strip_nulls(contacts.custom_fields || coalesce(sqlc.narg(custom_fields)::jsonb, '{}')),
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: UpdateContact :one
UPDATE contacts
SET name = CASE WHEN @set_name::bool THEN sqlc.narg(name) ELSE name END,
    language = CASE WHEN @set_language::bool THEN sqlc.narg(language) ELSE language END,
    custom_fields = jsonb_strip_nulls(custom_fields || coalesce(sqlc.narg(custom_fields)::jsonb, '{}')),
    blocked = coalesce(sqlc.narg(blocked), blocked),
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetContactConsent :one
UPDATE contacts
SET opt_in_status = @status,
    opted_in_at = CASE WHEN @status = 'opted_in'::opt_in_status THEN @at::timestamptz ELSE opted_in_at END,
    opted_out_at = CASE WHEN @status = 'opted_out'::opt_in_status THEN @at::timestamptz ELSE opted_out_at END,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: InsertConsentEvent :exec
INSERT INTO consent_events (tenant_id, contact_id, kind, source, evidence, recorded_by, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListConsentEvents :many
SELECT e.*, u.name AS recorded_by_name
FROM consent_events e LEFT JOIN users u ON u.id = e.recorded_by
WHERE e.contact_id = $1
ORDER BY e.occurred_at DESC
LIMIT 100;

-- name: UpsertTag :one
INSERT INTO tags (tenant_id, name) VALUES (@tenant_id, @name)
ON CONFLICT (tenant_id, name) DO UPDATE SET name = tags.name
RETURNING id;

-- name: AddContactTag :exec
INSERT INTO contact_tags (tenant_id, contact_id, tag_id) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveContactTag :exec
DELETE FROM contact_tags ct USING tags t
WHERE ct.tag_id = t.id AND ct.contact_id = @contact_id AND t.name = @name::citext;

-- name: ListTags :many
SELECT t.id, t.name, count(ct.contact_id) AS contacts
FROM tags t LEFT JOIN contact_tags ct ON ct.tag_id = t.id
GROUP BY t.id, t.name
ORDER BY t.name;
