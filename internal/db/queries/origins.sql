-- Origins and source devices the configurator lists (J15.6; docs/architecture/apple-health.md#origins-and-relays).

-- name: ListOwnerOrigins :many
SELECT o.id, p.code AS provider, o.origin_key, o.name, o.is_native, rp.code AS relayed_provider, o.created_at
FROM data_origins o
JOIN providers p ON p.id = o.provider_id
LEFT JOIN providers rp ON rp.id = o.relayed_provider_id
WHERE o.user_id = @user_id
ORDER BY p.code, o.origin_key;

-- name: ListRelayTargets :many
-- The vendors an origin can relay: those known_relay_origins already maps an origin to.
SELECT DISTINCT p.code, p.name FROM known_relay_origins k JOIN providers p ON p.id = k.relayed_provider_id ORDER BY p.name;

-- name: SetOriginRelay :execrows
-- Sets or clears (null) the vendor an origin relays. The edit wins over known_relay_origins, which
-- only seeds new origins (UpsertOrigin); a trigger clears the owner's resolved cache.
UPDATE data_origins o SET relayed_provider_id = (SELECT p.id FROM providers p WHERE p.code = sqlc.narg(relayed_provider)::text)
WHERE o.id = @id AND o.user_id = @user_id;

-- name: ListSourceDevices :many
SELECT d.id, p.code AS provider, d.fingerprint, d.name, d.device_type, d.manufacturer, d.model, d.merged_into
FROM devices d JOIN providers p ON p.id = d.provider_id
WHERE d.user_id = @user_id
ORDER BY p.code, d.device_type, d.model, d.id;

-- name: SourceDeviceRecords :many
-- Active records per device and connection, by table. Measurements count as in the inventory:
-- hourly aggregate rows (an interval once per hour it touches) plus daily values, so a request
-- never scans the measurements of a whole history.
WITH r AS (
  SELECT a.device_id, a.connection_id, 'measurements'::text AS kind, a.samples::bigint AS n
  FROM source_hourly_aggregates a WHERE a.user_id = @user_id AND a.device_id IS NOT NULL
  UNION ALL
  SELECT x.device_id, x.connection_id, 'measurements', 1 FROM measurements x
  WHERE x.user_id = @user_id AND x.kind = 'daily_value' AND x.device_id IS NOT NULL AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT x.device_id, x.connection_id, 'groups', 1 FROM measurement_groups x
  WHERE x.user_id = @user_id AND x.device_id IS NOT NULL AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT x.device_id, x.connection_id, 'sleep_sessions', 1 FROM sleep_sessions x
  WHERE x.user_id = @user_id AND x.device_id IS NOT NULL AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT x.device_id, x.connection_id, 'workouts', 1 FROM workouts x
  WHERE x.user_id = @user_id AND x.device_id IS NOT NULL AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT x.device_id, x.connection_id, 'events', 1 FROM health_events x
  WHERE x.user_id = @user_id AND x.device_id IS NOT NULL AND x.superseded_at IS NULL AND x.deleted_at IS NULL
)
SELECT r.device_id::uuid AS device_id, r.connection_id, r.kind, sum(r.n)::bigint AS n
FROM r GROUP BY 1, 2, 3 ORDER BY 1, 2, 3;

-- name: UpdateSourceDevice :execrows
-- Sets the fields flagged set_*; a type set here wins over the normalizer's (UpsertDevice), a
-- cleared one lets it fill the type again. A trigger clears the owner's resolved cache when the
-- type changes. Merged devices are not edited: their records live on the target.
UPDATE devices
SET device_type = CASE WHEN @set_type::boolean THEN sqlc.narg(device_type)::text ELSE device_type END,
    device_type_by_owner = CASE WHEN @set_type::boolean THEN sqlc.narg(device_type)::text IS NOT NULL ELSE device_type_by_owner END,
    name = CASE WHEN @set_name::boolean THEN sqlc.narg(name)::text ELSE name END
WHERE id = @id AND user_id = @user_id AND merged_into IS NULL;

-- name: LockDevicesForMerge :many
-- Locks both sides of a merge (and devices merged into the source), in id order.
SELECT id, provider_id, merged_into FROM devices
WHERE user_id = @user_id AND (id = ANY(@ids::uuid[]) OR merged_into = ANY(@ids::uuid[]))
ORDER BY id FOR UPDATE;

-- name: MarkDeviceDirty :exec
-- Marks every metric and local date the device's active measurements (and its sleep sessions,
-- through the sleep-derived metrics) feed, as a write would: the resolved cache rows that read
-- them are deleted and the hourly aggregates rebuilt. Workouts invalidate through their trigger.
INSERT INTO resolution_dirty (user_id, metric_id, local_date)
SELECT DISTINCT x.user_id, x.metric_id, x.local_date FROM measurements x
WHERE x.device_id = @device_id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
UNION
SELECT s.user_id, mc.id, s.sleep_date FROM sleep_sessions s JOIN metric_catalog mc ON mc.code = ANY(@sleep_codes::text[])
WHERE s.device_id = @device_id AND s.superseded_at IS NULL AND s.deleted_at IS NULL
ON CONFLICT (user_id, metric_id, local_date) DO UPDATE SET marked_at = EXCLUDED.marked_at;

-- name: MoveDeviceRecords :one
-- Repoints every row of every canonical table with a device_id (superseded and deleted rows too,
-- so history stays on one device) and counts them per table.
WITH m AS (UPDATE measurements t SET device_id = @target_id WHERE t.device_id = @device_id RETURNING 1),
g AS (UPDATE measurement_groups t SET device_id = @target_id WHERE t.device_id = @device_id RETURNING 1),
s AS (UPDATE sleep_sessions t SET device_id = @target_id WHERE t.device_id = @device_id RETURNING 1),
w AS (UPDATE workouts t SET device_id = @target_id WHERE t.device_id = @device_id RETURNING 1),
e AS (UPDATE health_events t SET device_id = @target_id WHERE t.device_id = @device_id RETURNING 1)
SELECT (SELECT count(*) FROM m)::bigint AS measurements, (SELECT count(*) FROM g)::bigint AS groups,
  (SELECT count(*) FROM s)::bigint AS sleep_sessions, (SELECT count(*) FROM w)::bigint AS workouts,
  (SELECT count(*) FROM e)::bigint AS events;

-- name: MergeDevice :exec
-- Points the device, and the devices already merged into it, at the target.
UPDATE devices SET merged_into = @target_id WHERE user_id = @user_id AND (id = @device_id OR merged_into = @device_id);
