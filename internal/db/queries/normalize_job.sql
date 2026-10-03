-- Normalization jobs (J07.5): batch normalize and reprocess.

-- name: LockRawForNormalize :one
-- Locks the raw row so two workers never normalize one payload at once. superseded: a newer raw
-- version of the same record exists, so this one must not write canonical rows.
SELECT r.id, r.connection_id, r.stream, r.external_key, r.content_type, r.fetched_at, r.request_meta,
       r.content_sha256, r.shape_fingerprint, r.status, r.normalizer_version_id, p.code AS provider,
       EXISTS (SELECT 1 FROM raw_payloads n WHERE n.supersedes_id = r.id) AS superseded
FROM raw_payloads r
JOIN connections c ON c.id = r.connection_id
JOIN providers p ON p.id = c.provider_id
WHERE r.id = @id
FOR UPDATE OF r;

-- name: ListBatchPendingRaw :many
SELECT id FROM raw_payloads WHERE batch_id = @batch_id AND status = 'stored' ORDER BY id;

-- name: SetRawNormalizeResult :exec
-- Records the outcome next to the status change made with ingest.SetStatus.
UPDATE raw_payloads
SET normalizer_version_id = @normalizer_version_id, normalized_at = now(),
    status_detail = @status_detail, warnings = @warnings
WHERE id = @id;

-- name: ListReprocessCandidates :many
-- Newest raw version of each record only: an older version's output was superseded on purpose.
SELECT r.id, r.stream, r.shape_fingerprint, r.normalizer_version_id
FROM raw_payloads r
WHERE r.id > @after AND r.status <> 'quarantined'
  AND (@stream::text = '' OR r.stream = @stream)
  AND (sqlc.narg(since)::timestamptz IS NULL OR r.fetched_at >= sqlc.narg(since))
  AND (sqlc.narg(until)::timestamptz IS NULL OR r.fetched_at < sqlc.narg(until))
  AND NOT EXISTS (SELECT 1 FROM raw_payloads n WHERE n.supersedes_id = r.id)
ORDER BY r.id
LIMIT @max_rows;
