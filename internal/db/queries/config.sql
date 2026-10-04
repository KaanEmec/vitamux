-- Owner configuration endpoints (J10.4, docs/architecture/api.md#owner-endpoints-apiv1). Every
-- query is scoped to the owner, directly or through the connection. Lists are keyset-paginated:
-- a NULL after_* starts at the beginning.

-- name: ListOwnerConnections :many
-- One connection when id is set, else all of the owner's.
SELECT c.id, p.code AS provider, c.mode, c.status, c.last_success_at, c.last_error_class, c.consecutive_failures,
       c.created_at, c.updated_at, r.blocked_until
FROM connections c JOIN providers p ON p.id = c.provider_id
LEFT JOIN provider_rate_state r ON r.provider_id = c.provider_id
WHERE c.user_id = @user_id AND (sqlc.narg(id)::uuid IS NULL OR c.id = sqlc.narg(id)::uuid)
ORDER BY c.created_at, c.id;

-- name: ProviderExists :one
SELECT EXISTS (SELECT 1 FROM providers WHERE code = @code);

-- name: InsertPushConnection :one
INSERT INTO connections (id, user_id, provider_id, mode, status)
SELECT @id, @user_id, p.id, 'push', 'active' FROM providers p WHERE p.code = @provider
RETURNING id;

-- name: EnsureManualConnection :one
-- The owner's one connection of provider manual: account_key is fixed, so the unique key finds it.
INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
SELECT @id, @user_id, p.id, @account_key, 'push', 'active' FROM providers p WHERE p.code = 'manual'
ON CONFLICT (user_id, provider_id, account_key) DO UPDATE SET account_key = excluded.account_key
RETURNING id;

-- name: SetOwnerConnectionStatus :execrows
-- Owner pause and resume: only from one of from_statuses, so system states are never overwritten.
UPDATE connections SET status = @status, updated_at = now()
WHERE id = @id AND user_id = @user_id AND status = ANY(@from_statuses::text[]);

-- name: ListOwnerSchedules :many
SELECT s.* FROM schedules s JOIN connections c ON c.id = s.connection_id
WHERE c.user_id = @user_id AND (sqlc.narg(connection_id)::uuid IS NULL OR s.connection_id = sqlc.narg(connection_id)::uuid)
ORDER BY s.connection_id, s.stream, s.mode;

-- name: GetOwnerSchedule :one
SELECT s.* FROM schedules s JOIN connections c ON c.id = s.connection_id
WHERE s.id = @id AND c.user_id = @user_id;

-- name: ListStreamCursors :many
SELECT stream, (cursor IS NOT NULL)::boolean AS has_cursor, high_watermark, status, status_reason, updated_at
FROM sync_cursors WHERE connection_id = @connection_id
ORDER BY stream;

-- name: ResetStreamCursor :exec
-- The next incremental sync starts from the connector's initial window.
UPDATE sync_cursors SET cursor = NULL, high_watermark = NULL, updated_at = now()
WHERE connection_id = @connection_id AND stream = @stream;

-- name: LockConnectionJobs :many
-- Locks the connection's active jobs until commit: workers skip locked queued jobs when claiming.
SELECT status FROM jobs WHERE connection_id = @connection_id AND status IN ('queued', 'running')
FOR UPDATE;

-- name: ListConnectionRuns :many
SELECT r.id, r.job_id, j.kind, r.attempt, r.started_at, r.finished_at, r.outcome, r.error_class, r.error_message, r.stats
FROM job_runs r JOIN jobs j ON j.id = r.job_id
WHERE j.connection_id = @connection_id AND (sqlc.narg(after_id)::bigint IS NULL OR r.id < sqlc.narg(after_id)::bigint)
ORDER BY r.id DESC
LIMIT @lim;

-- name: ListOwnerJobs :many
-- Newest first. Connection-less jobs (normalize, export, recompute) belong to the single owner.
SELECT j.* FROM jobs j LEFT JOIN connections c ON c.id = j.connection_id
WHERE (j.connection_id IS NULL OR c.user_id = @user_id)
  AND (sqlc.narg(status)::text IS NULL OR j.status = sqlc.narg(status)::text)
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (j.created_at, j.id) < (sqlc.narg(after_key)::timestamptz, @after_id::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT @lim;

-- name: GetJobsByID :many
SELECT * FROM jobs WHERE id = ANY(@ids::uuid[]) ORDER BY created_at, id;

-- name: ListOverridesPage :many
-- Newest first, revoked ones included.
SELECT * FROM manual_overrides
WHERE user_id = @user_id
  AND (sqlc.narg(metrics)::text[] IS NULL OR metric = ANY(sqlc.narg(metrics)::text[]))
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(after_key)::timestamptz, @after_id::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- Deleting a connection with its data (DELETE /connections/{id}?data=delete), in this order
-- inside one transaction; deleting the connection row then cascades credentials, schedules,
-- cursors, backfills, jobs and OAuth states.

-- name: MarkConnectionDirty :exec
-- The days of the connection's active rows must be resolved again once they are gone.
INSERT INTO resolution_dirty (user_id, metric_id, local_date)
SELECT m.user_id, m.metric_id, m.local_date FROM measurements m
WHERE m.connection_id = @connection_id AND m.superseded_at IS NULL
UNION
SELECT s.user_id, mc.id, s.sleep_date FROM sleep_sessions s JOIN metric_catalog mc ON mc.code = ANY(@sleep_codes::text[])
WHERE s.connection_id = @connection_id AND s.superseded_at IS NULL
ON CONFLICT (user_id, metric_id, local_date) DO UPDATE SET marked_at = now();

-- name: DeleteConnectionMeasurements :execrows
DELETE FROM measurements WHERE connection_id = @connection_id;

-- name: DeleteConnectionGroups :execrows
DELETE FROM measurement_groups WHERE connection_id = @connection_id;

-- name: DeleteConnectionSleep :execrows
DELETE FROM sleep_sessions WHERE connection_id = @connection_id;

-- name: DeleteConnectionWorkouts :execrows
DELETE FROM workouts WHERE connection_id = @connection_id;

-- name: DeleteConnectionEvents :execrows
DELETE FROM health_events WHERE connection_id = @connection_id;

-- name: ReleaseConnectionRawBlobs :exec
-- One blob reference per raw row goes away (hold blob.LockShared); the sweeper removes unreferenced files.
UPDATE blobs b SET refcount = b.refcount - c.n
FROM (SELECT rp.content_sha256, count(*)::integer AS n FROM raw_payloads rp WHERE rp.connection_id = @connection_id GROUP BY rp.content_sha256) c
WHERE b.sha256 = c.content_sha256;

-- name: DeleteConnectionImports :exec
DELETE FROM import_items
WHERE raw_payload_id IN (SELECT rp.id FROM raw_payloads rp WHERE rp.connection_id = @connection_id::uuid)
   OR import_run_id IN (SELECT ir.id FROM import_runs ir WHERE ir.connection_id = @connection_id::uuid);

-- name: DeleteConnectionImportRuns :exec
DELETE FROM import_runs WHERE connection_id = @connection_id;

-- name: DeleteConnectionRaw :execrows
DELETE FROM raw_payloads WHERE connection_id = @connection_id;

-- name: DeleteConnectionBatches :exec
DELETE FROM ingest_batches WHERE connection_id = @connection_id;

-- name: DeleteConnectionClients :exec
DELETE FROM clients WHERE connection_id = @connection_id;

-- name: DeleteOwnerConnection :execrows
DELETE FROM connections WHERE id = @id AND user_id = @user_id;
