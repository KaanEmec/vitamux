-- Connector runtime (J06.4, J06.5); see docs/architecture/connectors.md#runtime-responsibilities.

-- name: GetSyncConnection :one
SELECT c.id, c.user_id, c.status, c.config, p.code AS provider
FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.id = @id;

-- name: GetCredentials :one
SELECT ciphertext, version FROM credentials WHERE connection_id = @connection_id;

-- name: LockCredentials :one
-- Serializes refreshes of one connection's tokens until commit (single-flight across processes).
SELECT ciphertext, version FROM credentials WHERE connection_id = @connection_id FOR UPDATE;

-- name: UpsertCredentials :exec
INSERT INTO credentials (connection_id, ciphertext, key_id, access_expires_at)
VALUES (@connection_id, @ciphertext, @key_id, @access_expires_at)
ON CONFLICT (connection_id) DO UPDATE
SET ciphertext = excluded.ciphertext, key_id = excluded.key_id, access_expires_at = excluded.access_expires_at,
    version = credentials.version + 1, updated_at = now();

-- name: GetSyncCursor :one
SELECT cursor, high_watermark FROM sync_cursors WHERE connection_id = @connection_id AND stream = @stream;

-- name: AdvanceSyncCursor :exec
-- A null cursor keeps the stored one; the high watermark never moves backwards.
INSERT INTO sync_cursors (connection_id, stream, cursor, high_watermark)
VALUES (@connection_id, @stream, @cursor, @high_watermark)
ON CONFLICT (connection_id, stream) DO UPDATE
SET cursor = coalesce(excluded.cursor, sync_cursors.cursor),
    high_watermark = greatest(sync_cursors.high_watermark, excluded.high_watermark),
    updated_at = now();

-- name: SetStreamStatus :exec
INSERT INTO sync_cursors (connection_id, stream, status, status_reason)
VALUES (@connection_id, @stream, @status, @status_reason)
ON CONFLICT (connection_id, stream) DO UPDATE
SET status = excluded.status, status_reason = excluded.status_reason, updated_at = now();

-- name: RecordSyncSuccess :exec
-- The connection stays degraded while any of its streams is, except one whose schedules the
-- owner has all disabled.
UPDATE connections
SET status = CASE WHEN EXISTS (SELECT 1 FROM sync_cursors s WHERE s.connection_id = @id AND s.status = 'degraded'
                               AND (EXISTS (SELECT 1 FROM schedules x WHERE x.connection_id = s.connection_id
                                            AND x.stream = s.stream AND x.enabled)
                                    OR NOT EXISTS (SELECT 1 FROM schedules x WHERE x.connection_id = s.connection_id
                                                   AND x.stream = s.stream)))
                  THEN 'degraded' ELSE 'active' END,
    last_success_at = now(), last_error_class = NULL, consecutive_failures = 0, updated_at = now()
WHERE id = @id AND status IN ('active', 'degraded');

-- name: RecordSyncFailure :exec
-- A null status keeps the current one. Owner-set states (paused, disabled) are never overwritten.
UPDATE connections
SET status = CASE WHEN status IN ('active', 'degraded') THEN coalesce(sqlc.narg(status), status) ELSE status END,
    last_error_class = @error_class,
    consecutive_failures = consecutive_failures + @failures,
    updated_at = now()
WHERE id = @id;

-- name: GetProviderBlock :one
SELECT r.blocked_until FROM provider_rate_state r JOIN providers p ON p.id = r.provider_id
WHERE p.code = @provider;

-- name: BlockProvider :one
-- Extends, never shortens, the shared block.
INSERT INTO provider_rate_state (provider_id, blocked_until)
SELECT id, @blocked_until FROM providers WHERE code = @provider
ON CONFLICT (provider_id) DO UPDATE
SET blocked_until = greatest(provider_rate_state.blocked_until, excluded.blocked_until), updated_at = now()
RETURNING blocked_until;
