-- Data lifecycle (J13.4): retention pruning and the owner purge; see
-- docs/architecture/data-model.md#retention and docs/architecture/security.md#export-and-deletion.

-- name: ListLifecycleUsers :many
SELECT id, username FROM users ORDER BY id;

-- name: LockPrunableRaw :many
-- Raw payloads of one provider stored before cutoff that prune_raw may delete (data-model.md#retention):
-- the oldest remaining version of its record; reprocess would never use it (a newer version
-- exists) or it was normalized by the newest version of its normalizer; no active canonical row
-- references it; and no canonical row from it came from an older normalizer version. Rows a
-- worker holds (normalization) are skipped until the next run.
WITH stale AS (
  SELECT nv.id FROM normalizer_versions nv
  WHERE EXISTS (SELECT 1 FROM normalizer_versions n WHERE n.name = nv.name AND n.version > nv.version)
)
SELECT r.id, r.content_sha256
FROM raw_payloads r
JOIN connections c ON c.id = r.connection_id
JOIN providers p ON p.id = c.provider_id
WHERE r.user_id = @user_id AND p.code = @provider AND r.stored_at < @cutoff::timestamptz
  AND r.supersedes_id IS NULL
  AND (EXISTS (SELECT 1 FROM raw_payloads n WHERE n.supersedes_id = r.id)
       OR (r.status = 'normalized' AND r.normalizer_version_id IS NOT NULL
           AND r.normalizer_version_id NOT IN (SELECT id FROM stale)))
  AND NOT EXISTS (SELECT 1 FROM measurements x WHERE x.raw_payload_id = r.id
    AND ((x.superseded_at IS NULL AND x.deleted_at IS NULL) OR x.normalizer_version_id IN (SELECT id FROM stale)))
  AND NOT EXISTS (SELECT 1 FROM measurement_groups x WHERE x.raw_payload_id = r.id
    AND ((x.superseded_at IS NULL AND x.deleted_at IS NULL) OR x.normalizer_version_id IN (SELECT id FROM stale)))
  AND NOT EXISTS (SELECT 1 FROM sleep_sessions x WHERE x.raw_payload_id = r.id
    AND ((x.superseded_at IS NULL AND x.deleted_at IS NULL) OR x.normalizer_version_id IN (SELECT id FROM stale)))
  AND NOT EXISTS (SELECT 1 FROM workouts x WHERE x.raw_payload_id = r.id
    AND ((x.superseded_at IS NULL AND x.deleted_at IS NULL) OR x.normalizer_version_id IN (SELECT id FROM stale)))
  AND NOT EXISTS (SELECT 1 FROM health_events x WHERE x.raw_payload_id = r.id
    AND ((x.superseded_at IS NULL AND x.deleted_at IS NULL) OR x.normalizer_version_id IN (SELECT id FROM stale)))
ORDER BY r.id
LIMIT @max_rows
FOR UPDATE OF r SKIP LOCKED;

-- name: CountRawRefusedStale :one
-- Raw of one provider past cutoff that prune_raw refuses because it or a canonical row from it
-- was normalized by an older normalizer version.
WITH stale AS (
  SELECT nv.id FROM normalizer_versions nv
  WHERE EXISTS (SELECT 1 FROM normalizer_versions n WHERE n.name = nv.name AND n.version > nv.version)
)
SELECT count(*) FROM raw_payloads r
JOIN connections c ON c.id = r.connection_id
JOIN providers p ON p.id = c.provider_id
WHERE r.user_id = @user_id AND p.code = @provider AND r.stored_at < @cutoff::timestamptz
  AND (r.normalizer_version_id IN (SELECT id FROM stale)
    OR EXISTS (SELECT 1 FROM measurements x WHERE x.raw_payload_id = r.id AND x.normalizer_version_id IN (SELECT id FROM stale))
    OR EXISTS (SELECT 1 FROM measurement_groups x WHERE x.raw_payload_id = r.id AND x.normalizer_version_id IN (SELECT id FROM stale))
    OR EXISTS (SELECT 1 FROM sleep_sessions x WHERE x.raw_payload_id = r.id AND x.normalizer_version_id IN (SELECT id FROM stale))
    OR EXISTS (SELECT 1 FROM workouts x WHERE x.raw_payload_id = r.id AND x.normalizer_version_id IN (SELECT id FROM stale))
    OR EXISTS (SELECT 1 FROM health_events x WHERE x.raw_payload_id = r.id AND x.normalizer_version_id IN (SELECT id FROM stale)));

-- name: DetachPrunedRaw :exec
-- Clears every reference to raw payloads about to be pruned: inactive canonical rows keep their
-- values and history without a raw payload, import items stay done (re-imports still skip
-- them), and the next version of the record becomes the oldest.
WITH m AS (
  UPDATE measurements SET
    raw_payload_id = CASE WHEN raw_payload_id = ANY(@ids::bigint[]) THEN NULL ELSE raw_payload_id END,
    deleted_by_raw_id = CASE WHEN deleted_by_raw_id = ANY(@ids::bigint[]) THEN NULL ELSE deleted_by_raw_id END
  WHERE raw_payload_id = ANY(@ids::bigint[]) OR deleted_by_raw_id = ANY(@ids::bigint[])
), g AS (
  UPDATE measurement_groups SET
    raw_payload_id = CASE WHEN raw_payload_id = ANY(@ids::bigint[]) THEN NULL ELSE raw_payload_id END,
    deleted_by_raw_id = CASE WHEN deleted_by_raw_id = ANY(@ids::bigint[]) THEN NULL ELSE deleted_by_raw_id END
  WHERE raw_payload_id = ANY(@ids::bigint[]) OR deleted_by_raw_id = ANY(@ids::bigint[])
), s AS (
  UPDATE sleep_sessions SET
    raw_payload_id = CASE WHEN raw_payload_id = ANY(@ids::bigint[]) THEN NULL ELSE raw_payload_id END,
    deleted_by_raw_id = CASE WHEN deleted_by_raw_id = ANY(@ids::bigint[]) THEN NULL ELSE deleted_by_raw_id END
  WHERE raw_payload_id = ANY(@ids::bigint[]) OR deleted_by_raw_id = ANY(@ids::bigint[])
), w AS (
  UPDATE workouts SET
    raw_payload_id = CASE WHEN raw_payload_id = ANY(@ids::bigint[]) THEN NULL ELSE raw_payload_id END,
    deleted_by_raw_id = CASE WHEN deleted_by_raw_id = ANY(@ids::bigint[]) THEN NULL ELSE deleted_by_raw_id END
  WHERE raw_payload_id = ANY(@ids::bigint[]) OR deleted_by_raw_id = ANY(@ids::bigint[])
), e AS (
  UPDATE health_events SET
    raw_payload_id = CASE WHEN raw_payload_id = ANY(@ids::bigint[]) THEN NULL ELSE raw_payload_id END,
    deleted_by_raw_id = CASE WHEN deleted_by_raw_id = ANY(@ids::bigint[]) THEN NULL ELSE deleted_by_raw_id END
  WHERE raw_payload_id = ANY(@ids::bigint[]) OR deleted_by_raw_id = ANY(@ids::bigint[])
), i AS (
  UPDATE import_items SET raw_payload_id = NULL WHERE raw_payload_id = ANY(@ids::bigint[])
)
UPDATE raw_payloads SET supersedes_id = NULL WHERE supersedes_id = ANY(@ids::bigint[]);

-- name: ReleaseRawBlobs :exec
-- One blob reference per raw row goes away (hold blob.LockShared); the sweeper removes the files.
UPDATE blobs b SET refcount = b.refcount - c.n
FROM (SELECT content_sha256, count(*)::integer AS n FROM raw_payloads WHERE id = ANY(@ids::bigint[]) GROUP BY content_sha256) c
WHERE b.sha256 = c.content_sha256;

-- name: DeleteRawByIDs :execrows
DELETE FROM raw_payloads WHERE id = ANY(@ids::bigint[]);

-- Superseded rows go oldest end of each chain first: a row is deleted only when no older row
-- points at it (superseded_by points at the newer row), so no reference dangles and the active
-- head of a chain is never touched.

-- name: PruneSupersededMeasurements :execrows
DELETE FROM measurements WHERE id IN (
  SELECT m.id FROM measurements m
  WHERE m.user_id = @user_id AND m.superseded_at < @cutoff::timestamptz
    AND NOT EXISTS (SELECT 1 FROM measurements o WHERE o.superseded_by = m.id)
  LIMIT @max_rows);

-- name: PruneSupersededGroups :execrows
-- A group that a measurement still names as its group stays.
DELETE FROM measurement_groups WHERE id IN (
  SELECT g.id FROM measurement_groups g
  WHERE g.user_id = @user_id AND g.superseded_at < @cutoff::timestamptz
    AND NOT EXISTS (SELECT 1 FROM measurement_groups o WHERE o.superseded_by = g.id)
    AND NOT EXISTS (SELECT 1 FROM measurements m WHERE m.group_id = g.id)
  LIMIT @max_rows);

-- name: PruneSupersededSleep :execrows
DELETE FROM sleep_sessions WHERE id IN (
  SELECT s.id FROM sleep_sessions s
  WHERE s.user_id = @user_id AND s.superseded_at < @cutoff::timestamptz
    AND NOT EXISTS (SELECT 1 FROM sleep_sessions o WHERE o.superseded_by = s.id)
  LIMIT @max_rows);

-- name: PruneSupersededWorkouts :one
-- Releases the activity-file reference of each deleted row (hold blob.LockShared).
WITH d AS (
  DELETE FROM workouts WHERE id IN (
    SELECT w.id FROM workouts w
    WHERE w.user_id = @user_id AND w.superseded_at < @cutoff::timestamptz
      AND NOT EXISTS (SELECT 1 FROM workouts o WHERE o.superseded_by = w.id)
    LIMIT @max_rows)
  RETURNING file_blob_sha256
), r AS (
  UPDATE blobs b SET refcount = b.refcount - c.n
  FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM d WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
  WHERE b.sha256 = c.file_blob_sha256
)
SELECT count(*) FROM d;

-- name: PruneSupersededEvents :one
-- Releases the waveform or route reference of each deleted row (hold blob.LockShared).
WITH d AS (
  DELETE FROM health_events WHERE id IN (
    SELECT e.id FROM health_events e
    WHERE e.user_id = @user_id AND e.superseded_at < @cutoff::timestamptz
      AND NOT EXISTS (SELECT 1 FROM health_events o WHERE o.superseded_by = e.id)
    LIMIT @max_rows)
  RETURNING file_blob_sha256
), r AS (
  UPDATE blobs b SET refcount = b.refcount - c.n
  FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM d WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
  WHERE b.sha256 = c.file_blob_sha256
)
SELECT count(*) FROM d;

-- name: PruneIdempotencyKeys :execrows
DELETE FROM idempotency_keys k USING clients c
WHERE c.id = k.client_id AND c.user_id = @user_id AND k.created_at < @cutoff::timestamptz;

-- Purge (vitamux admin purge-user): everything of one owner, in dependency order.

-- name: LockPurgeJobs :many
-- The owner's jobs (connection, export and extraction jobs), locked so no worker claims one.
SELECT j.status FROM jobs j
WHERE j.connection_id IN (SELECT c.id FROM connections c WHERE c.user_id = @user_id)
   OR j.id IN (SELECT e.job_id FROM exports e WHERE e.user_id = @user_id)
   OR j.id IN (SELECT x.job_id FROM extraction_runs x WHERE x.user_id = @user_id)
FOR UPDATE OF j;

-- name: CountPurgeCascade :one
-- Rows the app role cannot delete itself; deleting the user removes them through the cascade.
SELECT (SELECT count(*) FROM resolution_rules r WHERE r.user_id = @user_id::uuid)::bigint AS rules,
       (SELECT count(*) FROM manual_overrides o WHERE o.user_id = @user_id::uuid)::bigint AS overrides;

-- name: PurgeMeasurements :execrows
DELETE FROM measurements WHERE user_id = @user_id;

-- name: PurgeGroups :execrows
DELETE FROM measurement_groups WHERE user_id = @user_id;

-- name: PurgeSleep :execrows
DELETE FROM sleep_sessions WHERE user_id = @user_id;

-- name: PurgeWorkouts :one
-- Releases each row's activity-file reference (hold blob.LockShared).
WITH d AS (DELETE FROM workouts WHERE user_id = @user_id RETURNING file_blob_sha256), r AS (
  UPDATE blobs b SET refcount = b.refcount - c.n
  FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM d WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
  WHERE b.sha256 = c.file_blob_sha256
)
SELECT count(*) FROM d;

-- name: PurgeEvents :one
-- Releases each row's waveform or route reference (hold blob.LockShared).
WITH d AS (DELETE FROM health_events WHERE user_id = @user_id RETURNING file_blob_sha256), r AS (
  UPDATE blobs b SET refcount = b.refcount - c.n
  FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM d WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
  WHERE b.sha256 = c.file_blob_sha256
)
SELECT count(*) FROM d;

-- name: PurgeResolutionDirty :execrows
DELETE FROM resolution_dirty WHERE user_id = @user_id;

-- name: PurgeImportItems :execrows
DELETE FROM import_items
WHERE import_run_id IN (SELECT ir.id FROM import_runs ir WHERE ir.user_id = @user_id::uuid)
   OR raw_payload_id IN (SELECT rp.id FROM raw_payloads rp WHERE rp.user_id = @user_id::uuid);

-- name: PurgeImportRuns :execrows
DELETE FROM import_runs WHERE user_id = @user_id;

-- name: ReleaseUserBlobs :exec
-- Drops the blob references of the owner's raw payloads, documents, extraction responses and
-- exports (hold blob.LockShared); the sweeper removes the files.
UPDATE blobs b SET refcount = b.refcount - c.n
FROM (
  SELECT sha256, count(*)::integer AS n FROM (
    SELECT rp.content_sha256 AS sha256 FROM raw_payloads rp WHERE rp.user_id = @user_id::uuid
    UNION ALL SELECT d.blob_sha256 FROM documents d WHERE d.user_id = @user_id::uuid AND d.blob_sha256 IS NOT NULL
    UNION ALL SELECT x.response_blob_sha256 FROM extraction_runs x WHERE x.user_id = @user_id::uuid AND x.response_blob_sha256 IS NOT NULL
    UNION ALL SELECT e.blob_sha256 FROM exports e WHERE e.user_id = @user_id::uuid AND e.blob_sha256 IS NOT NULL
  ) refs GROUP BY refs.sha256
) c
WHERE b.sha256 = c.sha256;

-- name: PurgeRaw :execrows
DELETE FROM raw_payloads WHERE user_id = @user_id;

-- name: PurgeBatches :execrows
DELETE FROM ingest_batches WHERE user_id = @user_id;

-- name: PurgeDocumentKeys :execrows
-- Crypto-shreds every document of the owner.
DELETE FROM document_keys WHERE document_id IN (SELECT d.id FROM documents d WHERE d.user_id = @user_id);

-- name: PurgeDocuments :execrows
-- Extraction runs, review rows and lab results go with them (cascade).
DELETE FROM documents WHERE user_id = @user_id;

-- name: PurgeExports :execrows
DELETE FROM exports WHERE user_id = @user_id;

-- name: PurgeOwnerJobs :execrows
-- Export and extraction jobs (no connection; their links are gone by now) of the given ids.
DELETE FROM jobs WHERE id = ANY(@ids::uuid[]);

-- name: ListPurgeJobIDs :many
SELECT e.job_id::uuid AS id FROM exports e WHERE e.user_id = @user_id::uuid AND e.job_id IS NOT NULL
UNION
SELECT x.job_id::uuid FROM extraction_runs x WHERE x.user_id = @user_id::uuid AND x.job_id IS NOT NULL;

-- name: PurgeClients :execrows
-- Their idempotency keys go with them (cascade).
DELETE FROM clients WHERE user_id = @user_id;

-- name: PurgeConnections :execrows
-- Credentials, schedules, cursors, backfills, jobs and OAuth states go with them (cascade).
DELETE FROM connections WHERE user_id = @user_id;

-- name: PurgeDevices :execrows
DELETE FROM devices WHERE user_id = @user_id;

-- name: PurgeOrigins :execrows
DELETE FROM data_origins WHERE user_id = @user_id;

-- name: PurgeSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id;

-- name: PurgeAPIKeys :execrows
DELETE FROM api_keys WHERE user_id = @user_id;

-- name: PurgeSettings :execrows
DELETE FROM settings WHERE user_id = @user_id;

-- name: PurgeProviderApps :execrows
-- Provider app credentials and panel sidecars the user entered (instance-wide rows, ADR-0021).
DELETE FROM provider_app_credentials WHERE updated_by = @user_id::uuid;

-- name: PurgeSidecars :execrows
DELETE FROM sidecars WHERE created_by = @user_id::uuid;

-- name: PurgeUser :execrows
-- Rules, active rules, overrides, timezone periods, recovery codes, OAuth states and custom
-- analyte aliases go with the user (cascade); audit events stay, with user_id set to null.
DELETE FROM users WHERE id = @user_id;
