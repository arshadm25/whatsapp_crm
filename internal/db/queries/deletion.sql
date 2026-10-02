-- name: InsertDeletionRequest :one
INSERT INTO data_deletion_requests (confirmation_code, meta_user_id) VALUES ($1, $2)
RETURNING *;

-- name: GetDeletionRequestByCode :one
SELECT * FROM data_deletion_requests WHERE confirmation_code = $1;

-- name: ListDeletionRequests :many
SELECT * FROM data_deletion_requests ORDER BY requested_at DESC LIMIT 100;

-- name: SetDeletionRequestStatus :one
UPDATE data_deletion_requests
SET status = @status::text, completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE NULL END
WHERE id = @id
RETURNING *;
