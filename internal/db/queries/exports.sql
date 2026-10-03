-- Exports and the NDJSON importer (J10.6); see internal/export.

-- ---------------------------------------------------------------- export rows

-- name: InsertExport :exec
INSERT INTO exports (id, user_id, job_id, format, include_raw) VALUES (@id, @user_id, @job_id, @format, @include_raw);

-- name: GetExport :one
-- Status comes from the job until the export finishes; a job that is gone or gave up is failed.
SELECT e.id, e.format, e.include_raw, e.created_at, e.finished_at, e.expires_at, e.size_bytes,
  (CASE WHEN e.finished_at IS NOT NULL THEN 'done'
        WHEN j.status IN ('queued', 'running') THEN j.status
        ELSE 'failed' END)::text AS status
FROM exports e LEFT JOIN jobs j ON j.id = e.job_id
WHERE e.id = @id AND e.user_id = @user_id;

-- name: GetExportForRun :one
SELECT user_id, format, include_raw, finished_at FROM exports WHERE id = @id;

-- name: FinishExport :exec
UPDATE exports SET finished_at = now(), expires_at = now() + @ttl::interval, size_bytes = @size_bytes, blob_sha256 = @blob_sha256
WHERE id = @id;

-- name: SetExportToken :execrows
UPDATE exports SET token_hash = @token_hash, token_expires_at = now() + @ttl::interval, token_used_at = NULL
WHERE id = @id AND user_id = @user_id AND finished_at IS NOT NULL AND expires_at > now();

-- name: UseExportToken :one
-- Single use: the first download with the current, unexpired token wins.
UPDATE exports SET token_used_at = now()
WHERE id = @id AND user_id = @user_id AND token_hash = @token_hash AND token_used_at IS NULL
  AND token_expires_at > now() AND expires_at > now()
RETURNING blob_sha256, size_bytes;

-- name: DeleteExpiredExports :many
DELETE FROM exports WHERE expires_at <= now() RETURNING blob_sha256;

-- ---------------------------------------------------------------- export reads
-- All run in one REPEATABLE READ, READ ONLY transaction, so the files are one consistent
-- snapshot. Rows are to_jsonb of the table row, keyset-paged in id order.

-- name: ExportSnapshot :exec
SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY;

-- name: ExportUTC :exec
SET LOCAL TIME ZONE 'UTC';

-- name: ExportProviders :many
SELECT to_jsonb(t)::jsonb AS row FROM providers t ORDER BY id;

-- name: ExportUnits :many
SELECT to_jsonb(t)::jsonb AS row FROM units t ORDER BY id;

-- name: ExportMetricCatalog :many
SELECT to_jsonb(t)::jsonb AS row FROM metric_catalog t ORDER BY id;

-- name: ExportNormalizerVersions :many
SELECT id::bigint AS id, to_jsonb(t)::jsonb AS row FROM normalizer_versions t
WHERE id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ExportTimezonePeriods :many
SELECT id, to_jsonb(t)::jsonb AS row FROM timezone_periods t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportSettings :many
SELECT to_jsonb(t)::jsonb AS row FROM settings t WHERE user_id = @user_id ORDER BY key;

-- name: ExportResolutionRules :many
SELECT id, to_jsonb(t)::jsonb AS row FROM resolution_rules t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportActiveRules :many
SELECT to_jsonb(t)::jsonb AS row FROM active_rules t WHERE user_id = @user_id ORDER BY metric;

-- name: ExportConnections :many
-- Without the webhook token hash: notifications are subscribed again after re-authorization.
SELECT id, (to_jsonb(t) - 'hook_token_hash')::jsonb AS row FROM connections t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportDevices :many
SELECT id, to_jsonb(t)::jsonb AS row FROM devices t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportDataOrigins :many
SELECT id, to_jsonb(t)::jsonb AS row FROM data_origins t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportClients :many
-- Without the token hash: imported clients must pair again.
SELECT id, (to_jsonb(t) - 'token_hash')::jsonb AS row FROM clients t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportIngestBatches :many
SELECT id, to_jsonb(t)::jsonb AS row FROM ingest_batches t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportRawPayloads :many
-- _blob carries the blob row, so an import without raw content can still keep the reference.
SELECT t.id, (to_jsonb(t) || jsonb_build_object('_blob', to_jsonb(b)))::jsonb AS row
FROM raw_payloads t JOIN blobs b ON b.sha256 = t.content_sha256
WHERE t.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportRawContent :many
SELECT id, content_sha256 FROM raw_payloads
WHERE user_id = @user_id AND id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ExportImportRuns :many
SELECT id, to_jsonb(t)::jsonb AS row FROM import_runs t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportImportItems :many
SELECT t.id, to_jsonb(t)::jsonb AS row FROM import_items t JOIN import_runs r ON r.id = t.import_run_id
WHERE r.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportMeasurementGroups :many
SELECT id, to_jsonb(t)::jsonb AS row FROM measurement_groups t
WHERE user_id = @user_id AND id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ExportMeasurements :many
SELECT id, to_jsonb(t)::jsonb AS row FROM measurements t
WHERE user_id = @user_id AND id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ExportSleepSessions :many
SELECT id, to_jsonb(t)::jsonb AS row FROM sleep_sessions t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportSleepStages :many
SELECT t.id, to_jsonb(t)::jsonb AS row FROM sleep_stages t JOIN sleep_sessions s ON s.id = t.session_id
WHERE s.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportWorkouts :many
SELECT t.id, (to_jsonb(t) || jsonb_build_object('_blob', to_jsonb(b)))::jsonb AS row
FROM workouts t LEFT JOIN blobs b ON b.sha256 = t.file_blob_sha256
WHERE t.user_id = @user_id AND t.id > @after::uuid ORDER BY t.id LIMIT @lim;

-- name: ExportWorkoutFiles :many
SELECT id, file_blob_sha256 FROM workouts
WHERE user_id = @user_id AND file_blob_sha256 IS NOT NULL AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportWorkoutSegments :many
SELECT t.workout_id, t.seq, to_jsonb(t)::jsonb AS row FROM workout_segments t JOIN workouts w ON w.id = t.workout_id
WHERE w.user_id = @user_id AND (t.workout_id, t.seq) > (@after_workout::uuid, @after_seq::integer)
ORDER BY t.workout_id, t.seq LIMIT @lim;

-- name: ExportAuditEvents :many
SELECT id, to_jsonb(t)::jsonb AS row FROM audit_events t
WHERE user_id = @user_id AND id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ExportManualOverrides :many
SELECT id, to_jsonb(t)::jsonb AS row FROM manual_overrides t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportMeasurementsCSV :many
-- Active measurements with catalogue codes, for spreadsheets.
SELECT m.id, mc.code AS metric, u.code AS unit, m.kind, m.start_at, m.end_at, m.tz_offset_min, m.local_date,
  m.value, m.source_value, su.code AS source_unit, p.code AS provider, m.connection_id, m.device_id,
  m.origin_id, m.group_id, m.external_id, m.quality_flags, m.raw_payload_id
FROM measurements m
JOIN metric_catalog mc ON mc.id = m.metric_id
JOIN units u ON u.id = mc.unit_id
JOIN providers p ON p.id = m.provider_id
LEFT JOIN units su ON su.id = m.source_unit_id
WHERE m.user_id = @user_id AND m.id > @after::bigint AND m.superseded_at IS NULL AND m.deleted_at IS NULL
ORDER BY m.id LIMIT @lim;

-- ---------------------------------------------------------------- import
-- (p).* rather than p.*, which sqlc rewrites.
-- @batch is a JSON array of exported rows, already patched by the importer (owner, offset ids,
-- remapped references). Rows that exist (same id, natural key, or an active row with the same
-- dedupe key) are skipped; the :many variants return the id each source row resolved to.

-- name: ImportCustomPlans :exec
-- The tables grow from empty within the import transaction: a cached generic plan made while
-- they were small would scan them whole for every batch.
SET LOCAL plan_cache_mode = force_custom_plan;

-- name: ImportOwners :many
SELECT id FROM users ORDER BY created_at LIMIT 2;

-- name: HasHealthData :one
SELECT EXISTS (SELECT 1 FROM connections) OR EXISTS (SELECT 1 FROM raw_payloads);

-- name: ReserveIDs :one
-- Moves the sequence past n ids and returns the offset to add to exported ids.
SELECT (setval(to_regclass(@seq::text), COALESCE(pg_sequence_last_value(to_regclass(@seq::text)), 0) + @n::bigint) - @n::bigint)::bigint AS off;

-- name: ImportNormalizerVersions :many
WITH ins AS (
  INSERT INTO normalizer_versions OVERRIDING SYSTEM VALUE
  SELECT (p).* FROM jsonb_populate_recordset(NULL::normalizer_versions, @batch::jsonb) p
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::bigint AS src_id, COALESCE(i.id, t.id)::bigint AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::integer
LEFT JOIN normalizer_versions t ON t.name = r->>'name' AND t.version = (r->>'version')::integer AND t.git_sha = r->>'git_sha';

-- name: ImportTimezonePeriods :execrows
INSERT INTO timezone_periods
SELECT (p).* FROM jsonb_populate_recordset(NULL::timezone_periods, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportSettings :execrows
INSERT INTO settings
SELECT (p).* FROM jsonb_populate_recordset(NULL::settings, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportResolutionRules :execrows
INSERT INTO resolution_rules
SELECT (p).* FROM jsonb_populate_recordset(NULL::resolution_rules, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportActiveRules :execrows
INSERT INTO active_rules
SELECT (p).* FROM jsonb_populate_recordset(NULL::active_rules, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportConnections :many
WITH ins AS (
  INSERT INTO connections
  SELECT (p).* FROM jsonb_populate_recordset(NULL::connections, @batch::jsonb) p
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::uuid AS src_id, COALESCE(i.id, t.id) AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::uuid
LEFT JOIN connections t ON t.id = (r->>'id')::uuid
  OR (t.user_id = (r->>'user_id')::uuid AND t.provider_id = (r->>'provider_id')::smallint
      AND t.account_key = (r->>'account_key')::bytea);

-- name: ImportDevices :many
WITH ins AS (
  INSERT INTO devices
  SELECT (p).* FROM jsonb_populate_recordset(NULL::devices, @batch::jsonb) p
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::uuid AS src_id, COALESCE(i.id, t.id) AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::uuid
LEFT JOIN devices t ON t.id = (r->>'id')::uuid
  OR (t.user_id = (r->>'user_id')::uuid AND t.provider_id = (r->>'provider_id')::smallint AND t.fingerprint = r->>'fingerprint');

-- name: ImportDataOrigins :many
WITH ins AS (
  INSERT INTO data_origins
  SELECT (p).* FROM jsonb_populate_recordset(NULL::data_origins, @batch::jsonb) p
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::uuid AS src_id, COALESCE(i.id, t.id) AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::uuid
LEFT JOIN data_origins t ON t.id = (r->>'id')::uuid
  OR (t.user_id = (r->>'user_id')::uuid AND t.provider_id = (r->>'provider_id')::smallint AND t.origin_key = r->>'origin_key');

-- name: ImportClients :execrows
INSERT INTO clients
SELECT (p).* FROM jsonb_populate_recordset(NULL::clients, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportIngestBatches :many
WITH ins AS (
  INSERT INTO ingest_batches
  SELECT (p).* FROM jsonb_populate_recordset(NULL::ingest_batches, @batch::jsonb) p
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::uuid AS src_id, COALESCE(i.id, t.id) AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::uuid
LEFT JOIN ingest_batches t ON t.id = (r->>'id')::uuid
  OR (t.client_id = (r->>'client_id')::uuid AND t.idempotency_key = r->>'idempotency_key');

-- name: ImportBlobs :execrows
-- Blob rows whose content the export did not include (refcount starts at 0; references add to it).
INSERT INTO blobs
SELECT (p).* FROM jsonb_populate_recordset(NULL::blobs, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportRawPayloads :many
-- supersedes_id prefers the target's previous version of the record, which a merge may
-- already hold; otherwise it is the (offset) exported id, possibly inserted by this statement.
WITH src AS (
  SELECT r || jsonb_build_object('supersedes_id', COALESCE(
    (SELECT t.id FROM raw_payloads t WHERE t.connection_id = (r->>'connection_id')::uuid AND t.stream = r->>'stream'
       AND t.external_key = r->>'external_key' AND t.version = (r->>'version')::integer - 1),
    (r->>'supersedes_id')::bigint)) AS r
  FROM jsonb_array_elements(@batch::jsonb) r
), ins AS (
  INSERT INTO raw_payloads OVERRIDING SYSTEM VALUE
  SELECT (p).* FROM src, jsonb_populate_record(NULL::raw_payloads, src.r) p
  ON CONFLICT DO NOTHING RETURNING id, content_sha256
), refs AS (
  UPDATE blobs b SET refcount = b.refcount + c.n
  FROM (SELECT content_sha256, count(*)::integer AS n FROM ins GROUP BY content_sha256) c
  WHERE b.sha256 = c.content_sha256
)
SELECT (r->>'id')::bigint AS src_id, COALESCE(i.id, t.id)::bigint AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::bigint
LEFT JOIN raw_payloads t ON t.connection_id = (r->>'connection_id')::uuid AND t.stream = r->>'stream'
  AND t.external_key = r->>'external_key' AND t.version = (r->>'version')::integer;

-- name: ImportImportRuns :execrows
INSERT INTO import_runs
SELECT (p).* FROM jsonb_populate_recordset(NULL::import_runs, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportImportItems :execrows
INSERT INTO import_items OVERRIDING SYSTEM VALUE
SELECT (p).* FROM jsonb_populate_recordset(NULL::import_items, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportMeasurementGroups :many
-- A dedupe key whose chain the target already has is skipped whole; skipped rows resolve to
-- the target's current row.
WITH ins AS (
  INSERT INTO measurement_groups OVERRIDING SYSTEM VALUE
  SELECT (p).* FROM jsonb_populate_recordset(NULL::measurement_groups, @batch::jsonb) p
  WHERE NOT EXISTS (SELECT 1 FROM measurement_groups t WHERE t.dedupe_key = p.dedupe_key AND t.superseded_at IS NULL)
  ON CONFLICT DO NOTHING RETURNING id
)
SELECT (r->>'id')::bigint AS src_id, COALESCE(i.id, t.id)::bigint AS id, (i.id IS NOT NULL)::boolean AS inserted
FROM jsonb_array_elements(@batch::jsonb) r
LEFT JOIN ins i ON i.id = (r->>'id')::bigint
LEFT JOIN measurement_groups t ON t.dedupe_key = (r->>'dedupe_key')::bytea AND t.superseded_at IS NULL;

-- name: ImportMeasurements :one
WITH ins AS (
  INSERT INTO measurements OVERRIDING SYSTEM VALUE
  SELECT (p).* FROM jsonb_populate_recordset(NULL::measurements, @batch::jsonb) p
  WHERE NOT EXISTS (SELECT 1 FROM measurements t WHERE t.dedupe_key = p.dedupe_key AND t.superseded_at IS NULL)
  ON CONFLICT DO NOTHING RETURNING user_id, metric_id, local_date
), dirty AS (
  INSERT INTO resolution_dirty (user_id, metric_id, local_date)
  SELECT DISTINCT user_id, metric_id, local_date FROM ins
  ON CONFLICT DO NOTHING
)
SELECT count(*) FROM ins;

-- name: ImportSleepSessions :many
INSERT INTO sleep_sessions
SELECT (p).* FROM jsonb_populate_recordset(NULL::sleep_sessions, @batch::jsonb) p
WHERE NOT EXISTS (SELECT 1 FROM sleep_sessions t WHERE t.dedupe_key = p.dedupe_key AND t.superseded_at IS NULL)
ON CONFLICT DO NOTHING RETURNING id;

-- name: ImportSleepStages :execrows
INSERT INTO sleep_stages OVERRIDING SYSTEM VALUE
SELECT (p).* FROM jsonb_populate_recordset(NULL::sleep_stages, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportWorkouts :many
WITH ins AS (
  INSERT INTO workouts
  SELECT (p).* FROM jsonb_populate_recordset(NULL::workouts, @batch::jsonb) p
  WHERE NOT EXISTS (SELECT 1 FROM workouts t WHERE t.dedupe_key = p.dedupe_key AND t.superseded_at IS NULL)
  ON CONFLICT DO NOTHING RETURNING id, file_blob_sha256
), refs AS (
  UPDATE blobs b SET refcount = b.refcount + c.n
  FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM ins WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
  WHERE b.sha256 = c.file_blob_sha256
)
SELECT id FROM ins;

-- name: ImportWorkoutSegments :execrows
INSERT INTO workout_segments
SELECT (p).* FROM jsonb_populate_recordset(NULL::workout_segments, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportAuditEvents :execrows
INSERT INTO audit_events OVERRIDING SYSTEM VALUE
SELECT (p).* FROM jsonb_populate_recordset(NULL::audit_events, @batch::jsonb) p;

-- name: ImportManualOverrides :execrows
INSERT INTO manual_overrides
SELECT (p).* FROM jsonb_populate_recordset(NULL::manual_overrides, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- Supersession links, set after the whole chain is in (superseded_by points at a newer row).

-- name: LinkImportedMeasurementGroups :exec
UPDATE measurement_groups t SET superseded_by = v.new_id
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@new_ids::bigint[]) AS new_id) v
WHERE t.id = v.id AND t.superseded_by IS NULL AND t.superseded_at IS NOT NULL;

-- name: LinkImportedMeasurements :exec
UPDATE measurements t SET superseded_by = v.new_id
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@new_ids::bigint[]) AS new_id) v
WHERE t.id = v.id AND t.superseded_by IS NULL AND t.superseded_at IS NOT NULL;

-- name: LinkImportedSleepSessions :exec
UPDATE sleep_sessions t SET superseded_by = v.new_id
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@new_ids::uuid[]) AS new_id) v
WHERE t.id = v.id AND t.superseded_by IS NULL AND t.superseded_at IS NOT NULL;

-- name: LinkImportedWorkouts :exec
UPDATE workouts t SET superseded_by = v.new_id
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@new_ids::uuid[]) AS new_id) v
WHERE t.id = v.id AND t.superseded_by IS NULL AND t.superseded_at IS NOT NULL;

-- name: RecordImportRun :exec
INSERT INTO import_runs (id, user_id, source, status, stats, finished_at)
VALUES (@id, @user_id, 'ndjson', 'done', @stats, now());
