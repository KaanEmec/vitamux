-- name: ClaimIdempotencyKey :execrows
-- A concurrent claim of the same key waits for the claiming transaction, then inserts nothing.
INSERT INTO idempotency_keys (client_id, key, request_sha256)
VALUES (@client_id, @key, @request_sha256)
ON CONFLICT (client_id, key) DO NOTHING;

-- name: SetIdempotencyResponse :one
-- Returns the body as stored (jsonb reorders keys), so the first answer equals every replay.
UPDATE idempotency_keys SET response_status = @response_status::smallint, response_body = @response_body::jsonb
WHERE client_id = @client_id AND key = @key
RETURNING response_body;

-- name: GetIdempotencyKey :one
SELECT request_sha256, response_status, response_body FROM idempotency_keys
WHERE client_id = @client_id AND key = @key;

-- name: GetIngestBatch :one
SELECT id, connection_id, received_at FROM ingest_batches WHERE id = @id;

-- name: ListBatchRaw :many
SELECT id, external_key, status FROM raw_payloads WHERE batch_id = @batch_id ORDER BY id;

-- name: LatestNormalizeJobStatus :one
SELECT status FROM jobs
WHERE kind = 'normalize_batch' AND payload->>'batch_id' = @batch_id::text
ORDER BY created_at DESC
LIMIT 1;

-- name: SetClientHeartbeat :execrows
-- Skips a heartbeat older than the one already recorded (sent_at), so a late retry cannot
-- roll the state back.
UPDATE clients SET metadata = metadata || jsonb_build_object('heartbeat', @heartbeat::jsonb)
WHERE id = @id
  AND (metadata->'heartbeat'->>'sent_at' IS NULL OR (metadata->'heartbeat'->>'sent_at')::timestamptz <= @sent_at::timestamptz);

-- name: UpsertPushCursor :exec
-- A push client's checkpoint for one stream, as reported in its heartbeat.
INSERT INTO sync_cursors (connection_id, stream, cursor, high_watermark, status, status_reason, updated_at)
VALUES (@connection_id, @stream, @cursor, @high_watermark, @status, @status_reason, now())
ON CONFLICT (connection_id, stream) DO UPDATE
SET cursor = COALESCE(EXCLUDED.cursor, sync_cursors.cursor),
    high_watermark = COALESCE(EXCLUDED.high_watermark, sync_cursors.high_watermark),
    status = EXCLUDED.status, status_reason = EXCLUDED.status_reason, updated_at = now();
