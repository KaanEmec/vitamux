-- Device pairing and paired devices (J15.2; docs/architecture/apple-health.md#pairing-and-security).

-- name: InsertPairingCode :execrows
-- Inserts nothing once the owner created @max_recent codes since @since (the rate limit).
INSERT INTO pairing_codes (id, user_id, code_hash, created_at, expires_at)
SELECT @id, @user_id, @code_hash, @now::timestamptz, @expires_at::timestamptz
WHERE (SELECT count(*) FROM pairing_codes p WHERE p.user_id = @user_id AND p.created_at > @since::timestamptz) < @max_recent::bigint;

-- name: DeleteOldPairingCodes :exec
DELETE FROM pairing_codes WHERE user_id = @user_id AND expires_at < @before::timestamptz;

-- name: UsePairingCode :one
-- Single use: marks the code used and returns its owner; no row when it is unknown, used or expired.
UPDATE pairing_codes SET used_at = @now::timestamptz
WHERE code_hash = @code_hash AND used_at IS NULL AND expires_at > @now::timestamptz
RETURNING user_id;

-- name: OwnerApplePushConnection :one
-- The owner's oldest apple_health push connection that is not disabled; paired devices share it.
SELECT c.id FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.user_id = @user_id AND p.code = 'apple_health' AND c.mode = 'push' AND c.status <> 'disabled'
ORDER BY c.created_at, c.id
LIMIT 1;

-- name: SetClientToken :execrows
UPDATE clients SET token_hash = @token_hash WHERE id = @id AND revoked_at IS NULL;

-- name: ListOwnerDevices :many
-- last_sync_at (null or a time) is the newest batch the device uploaded; checkpoint is its connection's
-- healthkit.samples.v1 heartbeat checkpoint (per-type anchor hashes), when it sent one.
SELECT cl.id, cl.connection_id, cl.name, cl.created_at, cl.last_seen_at, cl.revoked_at, cl.anchor_resets,
  (SELECT max(b.received_at) FROM ingest_batches b WHERE b.client_id = cl.id) AS last_sync_at, sc.cursor AS checkpoint
FROM clients cl
LEFT JOIN sync_cursors sc ON sc.connection_id = cl.connection_id AND sc.stream = 'healthkit.samples.v1'
WHERE cl.user_id = @user_id AND cl.kind = 'device'
ORDER BY cl.created_at DESC, cl.id;

-- name: RevokeOwnerDevice :execrows
UPDATE clients SET revoked_at = @now::timestamptz
WHERE id = @id AND user_id = @user_id AND kind = 'device' AND revoked_at IS NULL;

-- name: AddAnchorResets :execrows
UPDATE clients SET anchor_resets = anchor_resets || @resets::jsonb
WHERE id = @id AND user_id = @user_id AND kind = 'device' AND revoked_at IS NULL;

-- name: ListActiveHealthKitTypes :many
-- The HealthKit types (external_key is '<type>:<batch key>') each connection stored a payload for
-- since @since; a requested type missing here has been silent for that long.
SELECT DISTINCT r.connection_id, split_part(r.external_key, ':', 1)::text AS type
FROM raw_payloads r
WHERE r.connection_id = ANY(@connection_ids::uuid[]) AND r.stream = 'healthkit.samples.v1' AND r.stored_at >= @since::timestamptz;
