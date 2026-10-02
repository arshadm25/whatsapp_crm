-- name: ListConversations :many
SELECT sqlc.embed(cv), sqlc.embed(ct), coalesce(cv.last_message_at, cv.created_at)::timestamptz AS activity_at
FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
WHERE (sqlc.narg(phone_number_id)::uuid IS NULL OR cv.phone_number_id = sqlc.narg(phone_number_id))
  AND (sqlc.narg(status)::conversation_status IS NULL OR cv.status = sqlc.narg(status))
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR cv.assignee_user_id = sqlc.narg(assignee_id))
  AND (NOT @unassigned::boolean OR cv.assignee_user_id IS NULL)
  AND (sqlc.narg(window_open)::boolean IS NULL
       OR coalesce(cv.last_inbound_at > now() - interval '24 hours', false) = sqlc.narg(window_open))
  AND (sqlc.narg(search)::text IS NULL
       OR ct.wa_id LIKE '%' || sqlc.narg(search) || '%'
       OR ct.name ILIKE '%' || sqlc.narg(search) || '%'
       OR ct.profile_name ILIKE '%' || sqlc.narg(search) || '%')
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (coalesce(cv.last_message_at, cv.created_at), cv.id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY coalesce(cv.last_message_at, cv.created_at) DESC, cv.id DESC
LIMIT @lim;

-- name: GetConversationView :one
SELECT sqlc.embed(cv), sqlc.embed(ct)
FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
WHERE cv.id = $1;

-- name: UpdateConversation :exec
UPDATE conversations
SET status = coalesce(sqlc.narg(status), status),
    assignee_user_id = CASE WHEN @set_assignee::boolean THEN sqlc.narg(assignee_user_id) ELSE assignee_user_id END,
    updated_at = now()
WHERE id = @id;

-- name: ListConversationMessages :many
SELECT * FROM messages
WHERE conversation_id = @conversation_id AND (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before))
ORDER BY id DESC
LIMIT @lim;

-- name: ListContactTags :many
SELECT ct.contact_id, t.name FROM contact_tags ct JOIN tags t ON t.id = ct.tag_id
WHERE ct.contact_id = ANY(@contact_ids::uuid[])
ORDER BY t.name;

-- name: ListMembers :many
SELECT u.id, u.name, u.email, m.role, m.created_at
FROM memberships m JOIN users u ON u.id = m.user_id
ORDER BY u.name;

-- name: IsMember :one
SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id = $1)::boolean;

-- name: ListNotes :many
SELECT n.id, n.body, n.created_at, n.author_user_id, u.name AS author_name
FROM conversation_notes n JOIN users u ON u.id = n.author_user_id
WHERE n.conversation_id = $1
ORDER BY n.created_at;

-- name: InsertNote :one
INSERT INTO conversation_notes (id, tenant_id, conversation_id, author_user_id, body)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListQuickReplies :many
SELECT * FROM quick_replies ORDER BY shortcut;

-- name: InsertQuickReply :one
INSERT INTO quick_replies (id, tenant_id, shortcut, body, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteQuickReply :execrows
DELETE FROM quick_replies WHERE id = $1;

-- name: ConversationCounts :one
-- The inbox tab counts and the unread badge. Mine needs a team member; API keys pass NULL.
SELECT (count(*) FILTER (WHERE status = 'open'))::int AS open,
       (count(*) FILTER (WHERE status = 'open' AND assignee_user_id = sqlc.narg(user_id)::uuid))::int AS mine,
       (count(*) FILTER (WHERE status = 'open' AND assignee_user_id IS NULL))::int AS unassigned,
       (count(*) FILTER (WHERE status = 'pending'))::int AS pending,
       (count(*) FILTER (WHERE status = 'closed'))::int AS closed,
       (count(*) FILTER (WHERE unread_count > 0))::int AS unread_conversations,
       coalesce(sum(unread_count), 0)::int AS unread_messages
FROM conversations
WHERE sqlc.narg(phone_number_id)::uuid IS NULL OR phone_number_id = sqlc.narg(phone_number_id);
