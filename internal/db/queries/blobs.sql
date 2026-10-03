-- name: LockBlobsShared :exec
-- Writers (Put, Retain) hold this until commit; the sweeper takes it exclusively.
SELECT pg_advisory_xact_lock_shared(@key::bigint);

-- name: LockBlobsExclusive :exec
SELECT pg_advisory_xact_lock(@key::bigint);

-- name: InsertBlob :execrows
INSERT INTO blobs (sha256, size_bytes, stored_bytes, compression, key_id)
VALUES (@sha256, @size_bytes, @stored_bytes, @compression, @key_id)
ON CONFLICT (sha256) DO NOTHING;

-- name: GetBlob :one
SELECT * FROM blobs WHERE sha256 = @sha256;

-- name: AddBlobRef :one
UPDATE blobs SET refcount = refcount + @delta::integer WHERE sha256 = @sha256 RETURNING refcount;

-- name: DeleteUnreferencedBlobs :many
DELETE FROM blobs WHERE refcount = 0 AND created_at < @before::timestamptz RETURNING sha256;

-- name: ListBlobHashes :many
SELECT sha256 FROM blobs;
