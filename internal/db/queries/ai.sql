-- name: InsertKBSource :one
INSERT INTO kb_sources (id, tenant_id, kind, title, url, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetKBSource :one
SELECT * FROM kb_sources WHERE id = $1;

-- name: GetKBSourceForUpdate :one
SELECT * FROM kb_sources WHERE id = $1 FOR UPDATE;

-- name: ListKBSources :many
SELECT * FROM kb_sources ORDER BY created_at DESC, id DESC;

-- name: DeleteKBSource :execrows
DELETE FROM kb_sources WHERE id = $1;

-- name: FinishKBSource :one
UPDATE kb_sources
SET status = @status, error = @error, chunk_count = @chunk_count, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: MarkKBSourcePending :exec
UPDATE kb_sources SET status = 'pending', error = NULL, updated_at = now() WHERE id = $1;

-- name: InsertKBChunk :exec
INSERT INTO kb_chunks (id, tenant_id, source_id, ord, content) VALUES ($1, $2, $3, $4, $5);

-- name: DeleteKBChunks :exec
DELETE FROM kb_chunks WHERE source_id = $1;

-- name: CountKBChunks :one
SELECT count(*) FROM kb_chunks;

-- name: SearchKBChunks :many
-- Full-text search over the workspace's knowledge; the query is a to_tsquery string of OR-ed words.
SELECT c.id, c.source_id, c.content, s.title,
       ts_rank(c.tsv, to_tsquery('simple', @query::text)) AS rank
FROM kb_chunks c JOIN kb_sources s ON s.id = c.source_id
WHERE c.tsv @@ to_tsquery('simple', @query::text)
ORDER BY rank DESC, c.id
LIMIT @lim;

-- name: ListKBChunksForScan :many
-- A bounded scan for languages the full-text parser does not split into words.
SELECT c.id, c.source_id, c.content, s.title
FROM kb_chunks c JOIN kb_sources s ON s.id = c.source_id
ORDER BY c.source_id, c.ord
LIMIT @lim;

-- name: InsertAILog :exec
INSERT INTO ai_logs (id, tenant_id, bot_id, conversation_id, question, answer, confidence, outcome, source_ids, input_tokens, output_tokens)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: CountAIReplies :one
SELECT count(*) FROM ai_logs WHERE outcome IN ('answered', 'low_confidence', 'test') AND created_at >= @since::timestamptz;

-- name: ListAILogs :many
SELECT * FROM ai_logs
WHERE (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at), sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;
