-- name: ListNotifications :many
SELECT * FROM notifications WHERE user_id = @user_id AND in_app ORDER BY created_at DESC, id DESC LIMIT @lim;

-- name: CountUnreadNotifications :one
SELECT count(*)::int FROM notifications WHERE user_id = @user_id AND in_app AND read_at IS NULL;

-- name: MarkNotificationsRead :exec
UPDATE notifications SET read_at = now()
WHERE user_id = @user_id AND in_app AND read_at IS NULL AND (sqlc.narg(ids)::uuid[] IS NULL OR id = ANY(sqlc.narg(ids)::uuid[]));

-- name: ListNotificationSettings :many
SELECT * FROM notification_settings WHERE user_id = @user_id;

-- name: UpsertNotificationSetting :exec
INSERT INTO notification_settings (tenant_id, user_id, kind, in_app, email)
VALUES (@tenant_id, @user_id, @kind, @in_app, @email)
ON CONFLICT (tenant_id, user_id, kind) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email;

-- name: NotificationEmailTenants :many
SELECT t::uuid AS tenant_id FROM notification_email_tenants() t;

-- name: ClaimNotificationEmails :many
SELECT n.id, n.title, n.body, n.link, u.email AS user_email, u.name AS user_name
FROM notifications n JOIN users u ON u.id = n.user_id
WHERE n.email = 'pending'
ORDER BY n.created_at
LIMIT @lim
FOR UPDATE OF n SKIP LOCKED;

-- name: SetNotificationEmail :exec
UPDATE notifications SET email = @email WHERE id = @id;

