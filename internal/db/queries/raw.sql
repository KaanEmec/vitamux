-- name: InsertIngestBatch :exec
INSERT INTO ingest_batches (id, user_id, connection_id, client_id, source_kind, migration_source, idempotency_key)
VALUES (@id, @user_id, @connection_id, @client_id, @source_kind, @migration_source, @idempotency_key);

-- name: LockRawKey :exec
-- Serializes versioning of one (connection, stream, external_key) until commit.
SELECT pg_advisory_xact_lock(@class::integer, hashtext(@key::text));

-- name: LatestRawVersion :one
SELECT id, version, content_sha256 FROM raw_payloads
WHERE connection_id = @connection_id AND stream = @stream AND external_key = @external_key
ORDER BY version DESC
LIMIT 1;

-- name: InsertRawPayload :one
INSERT INTO raw_payloads (user_id, connection_id, batch_id, stream, external_key, version, supersedes_id,
                          content_sha256, content_type, fetched_at, request_meta, shape_fingerprint, status)
VALUES (@user_id, @connection_id, @batch_id, @stream, @external_key, @version, @supersedes_id,
        @content_sha256, @content_type, @fetched_at, @request_meta, @shape_fingerprint, @status)
RETURNING id;

-- name: SetRawStatus :execrows
UPDATE raw_payloads SET status = @status WHERE id = @id AND status = ANY(@from_status::text[]);

-- name: GetRawStatus :one
SELECT status FROM raw_payloads WHERE id = @id;
