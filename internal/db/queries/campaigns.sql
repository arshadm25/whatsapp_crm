-- name: InsertCampaign :one
INSERT INTO campaigns (id, tenant_id, phone_number_id, template_id, name, audience, variables, status,
                       scheduled_at, send_rate_per_min, created_by, api_key_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateCampaign :one
-- Replaces a draft or scheduled campaign's settings and invalidates its queued run job.
UPDATE campaigns
SET phone_number_id = @phone_number_id, template_id = @template_id, name = @name, audience = @audience,
    variables = @variables, status = @status, scheduled_at = sqlc.narg(scheduled_at),
    send_rate_per_min = sqlc.narg(send_rate_per_min), run_generation = run_generation + 1, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetCampaignStatus :one
-- Pauses or resumes a campaign. The generation changes so only the job queued now runs.
UPDATE campaigns SET status = @status, run_generation = run_generation + 1, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteDraftCampaign :execrows
DELETE FROM campaigns WHERE id = $1 AND status = 'draft';

-- name: CampaignCounts :many
SELECT status, count(*)::int AS n FROM campaigns
WHERE sqlc.narg(phone_number_id)::uuid IS NULL OR phone_number_id = sqlc.narg(phone_number_id)
GROUP BY status;

-- name: GetCampaign :one
SELECT * FROM campaigns WHERE id = $1;

-- name: GetCampaignForUpdate :one
SELECT * FROM campaigns WHERE id = $1 FOR UPDATE;

-- name: ListCampaigns :many
SELECT * FROM campaigns
WHERE (sqlc.narg(phone_number_id)::uuid IS NULL OR phone_number_id = sqlc.narg(phone_number_id))
  AND (sqlc.narg(statuses)::text[] IS NULL OR status::text = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: CampaignStats :many
-- Recipient counts per campaign. sent, delivered and read are cumulative: a read message was
-- also delivered and sent.
SELECT campaign_id,
       count(*)::int AS total,
       (count(*) FILTER (WHERE status = 'pending'))::int AS pending,
       (count(*) FILTER (WHERE status = 'skipped'))::int AS skipped,
       (count(*) FILTER (WHERE status = 'queued'))::int AS queued,
       (count(*) FILTER (WHERE status IN ('sent', 'delivered', 'read')))::int AS sent,
       (count(*) FILTER (WHERE status IN ('delivered', 'read')))::int AS delivered,
       (count(*) FILTER (WHERE status = 'read'))::int AS read,
       (count(*) FILTER (WHERE status = 'failed'))::int AS failed
FROM campaign_recipients
WHERE campaign_id = ANY(@ids::uuid[])
GROUP BY campaign_id;

-- name: AudienceCounts :one
-- How many contacts an audience reaches, and why the others would be skipped.
SELECT count(*)::int AS total,
       (count(*) FILTER (WHERE NOT c.blocked AND c.opt_in_status = 'opted_in'))::int AS eligible,
       (count(*) FILTER (WHERE c.blocked))::int AS blocked,
       (count(*) FILTER (WHERE NOT c.blocked AND c.opt_in_status = 'opted_out'))::int AS opted_out,
       (count(*) FILTER (WHERE NOT c.blocked AND c.opt_in_status = 'unknown'))::int AS no_opt_in
FROM contacts c
WHERE c.id = ANY(@contact_ids::uuid[])
   OR EXISTS (SELECT 1 FROM contact_tags ct JOIN tags t ON t.id = ct.tag_id
              WHERE ct.contact_id = c.id AND t.name = ANY(@tags::citext[]));

-- name: ExpandCampaignAudience :execrows
-- Adds every contact in the audience once. Blocked, opted-out and never-opted-in contacts are
-- recorded as skipped with the reason.
INSERT INTO campaign_recipients (tenant_id, campaign_id, contact_id, status, skip_reason)
SELECT c.tenant_id, @campaign_id, c.id,
       CASE WHEN c.blocked OR c.opt_in_status <> 'opted_in' THEN 'skipped' ELSE 'pending' END::recipient_status,
       CASE WHEN c.blocked THEN 'blocked'
            WHEN c.opt_in_status = 'opted_out' THEN 'opted_out'
            WHEN c.opt_in_status <> 'opted_in' THEN 'no_opt_in' END
FROM contacts c
WHERE c.id = ANY(@contact_ids::uuid[])
   OR EXISTS (SELECT 1 FROM contact_tags ct JOIN tags t ON t.id = ct.tag_id
              WHERE ct.contact_id = c.id AND t.name = ANY(@tags::citext[]))
ON CONFLICT DO NOTHING;

-- name: StartCampaign :exec
UPDATE campaigns SET status = 'running', started_at = now(), updated_at = now() WHERE id = $1;

-- name: FinishCampaign :exec
UPDATE campaigns SET status = @status, finished_at = now(), updated_at = now() WHERE id = @id;

-- name: SkipPendingRecipients :exec
UPDATE campaign_recipients SET status = 'skipped', skip_reason = @reason, updated_at = now()
WHERE campaign_id = @campaign_id AND status = 'pending';

-- name: NextCampaignRecipients :many
-- The next pending recipients to send to, with their contact.
SELECT sqlc.embed(c)
FROM campaign_recipients r JOIN contacts c ON c.id = r.contact_id
WHERE r.campaign_id = @campaign_id AND r.status = 'pending'
ORDER BY r.contact_id
LIMIT @lim;

-- name: HasPendingRecipients :one
SELECT EXISTS (SELECT 1 FROM campaign_recipients WHERE campaign_id = $1 AND status = 'pending');

-- name: QueueCampaignRecipient :exec
UPDATE campaign_recipients SET status = 'queued', message_id = @message_id, updated_at = now()
WHERE campaign_id = @campaign_id AND contact_id = @contact_id;

-- name: SkipCampaignRecipient :exec
UPDATE campaign_recipients SET status = 'skipped', skip_reason = @reason, updated_at = now()
WHERE campaign_id = @campaign_id AND contact_id = @contact_id;

-- name: BusinessInitiatedToday :one
-- Distinct customers sent a template from this number in the last 24 hours: what Meta's
-- messaging limit counts.
SELECT count(DISTINCT contact_id)::int FROM messages
WHERE phone_number_id = $1 AND direction = 'outbound' AND type = 'template'
  AND status <> 'failed' AND created_at > now() - interval '24 hours';

-- name: InsertCampaignMessage :one
INSERT INTO messages (id, tenant_id, conversation_id, phone_number_id, contact_id, direction, origin,
                      type, content, template_id, status, campaign_id)
VALUES ($1, $2, $3, $4, $5, 'outbound', 'campaign', 'template', $6, $7, 'queued', $8)
RETURNING *;

-- name: ListCampaignRecipients :many
SELECT r.contact_id, r.status, r.skip_reason, r.message_id, r.error_code, r.updated_at,
       c.wa_id, coalesce(c.name, c.profile_name) AS contact_name
FROM campaign_recipients r JOIN contacts c ON c.id = r.contact_id
WHERE r.campaign_id = @campaign_id
  AND (sqlc.narg(status)::recipient_status IS NULL OR r.status = sqlc.narg(status))
  AND (sqlc.narg(after_id)::uuid IS NULL OR r.contact_id > sqlc.narg(after_id))
ORDER BY r.contact_id
LIMIT @lim;

