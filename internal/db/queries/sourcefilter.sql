-- Apple Health source filter (J22.25; docs/architecture/apple-health.md#source-filter).

-- name: GetDeviceSourceFilter :one
SELECT cl.id, cl.user_id, cl.revoked_at, cl.source_filter, cl.source_filter_version, cl.health_sources, cl.health_sources_at
FROM clients cl
WHERE cl.id = @id AND cl.user_id = @user_id AND cl.kind = 'device';

-- name: LockDeviceSourceFilter :one
SELECT cl.id, cl.user_id, cl.revoked_at, cl.source_filter, cl.source_filter_version, cl.health_sources, cl.health_sources_at
FROM clients cl
WHERE cl.id = @id AND cl.user_id = @user_id AND cl.kind = 'device'
FOR UPDATE;

-- name: SetDeviceSourceFilter :one
UPDATE clients SET source_filter = @source_filter, source_filter_version = source_filter_version + 1
WHERE id = @id AND kind = 'device'
RETURNING source_filter_version;

-- name: SetDeviceHealthSources :execrows
-- What the device found in Apple Health (bundle ids, names, types and last sample times).
UPDATE clients SET health_sources = @health_sources, health_sources_at = @reported_at::timestamptz
WHERE id = @id AND kind = 'device' AND revoked_at IS NULL;

-- name: ListSourceFilterDefaults :many
-- The origins ignored by default: Apple Health origins that relay a provider the owner connects
-- directly (a connection that is not disabled). A seen origin's classification
-- (data_origins.relayed_provider_id, owner edits included) wins over known_relay_origins, which
-- covers the origins not seen yet. Patterns are LIKE patterns with backslash escapes.
SELECT x.pattern::text AS pattern, p.code AS provider, p.name AS provider_name
FROM (
  SELECT replace(replace(replace(o.origin_key, '\', '\\'), '%', '\%'), '_', '\_') AS pattern, o.relayed_provider_id AS relayed
  FROM data_origins o JOIN providers t ON t.id = o.provider_id AND t.code = 'apple_health'
  WHERE o.user_id = @user_id AND o.relayed_provider_id IS NOT NULL
  UNION
  SELECT k.origin_pattern, k.relayed_provider_id
  FROM known_relay_origins k JOIN providers t ON t.id = k.provider_id AND t.code = 'apple_health'
  WHERE NOT EXISTS (SELECT 1 FROM data_origins o WHERE o.user_id = @user_id AND o.provider_id = k.provider_id AND o.origin_key LIKE k.origin_pattern)
) x
JOIN providers p ON p.id = x.relayed
WHERE EXISTS (SELECT 1 FROM connections c WHERE c.user_id = @user_id AND c.provider_id = x.relayed AND c.status <> 'disabled')
ORDER BY 1, 2;

-- name: ListAppleOrigins :many
-- The owner's Apple Health origins with their classification and the records held raw because a
-- filter ignored them.
SELECT o.id, o.origin_key, o.name, o.is_native, rp.code AS relayed_provider,
  coalesce((SELECT sum(i.records) FROM ignored_records i WHERE i.origin_id = o.id), 0)::bigint AS ignored_records
FROM data_origins o
JOIN providers p ON p.id = o.provider_id AND p.code = 'apple_health'
LEFT JOIN providers rp ON rp.id = o.relayed_provider_id
WHERE o.user_id = @user_id
ORDER BY o.origin_key;

-- name: GetRawDeviceFilter :one
-- The source filter of the paired device that pushed a raw payload; no row for other senders.
SELECT cl.id, cl.user_id, cl.source_filter, cl.source_filter_version
FROM raw_payloads r
JOIN ingest_batches b ON b.id = r.batch_id
JOIN clients cl ON cl.id = b.client_id
WHERE r.id = @id AND cl.kind = 'device';

-- name: DeleteIgnoredRecords :exec
DELETE FROM ignored_records WHERE raw_payload_id = @raw_payload_id;

-- name: InsertIgnoredRecords :exec
-- One row per origin and item; origins are the payload's provider's, written by the same normalization.
INSERT INTO ignored_records (raw_payload_id, origin_id, user_id, item_kind, item_code, records, first_at, last_at)
SELECT r.id, o.id, r.user_id, x.item_kind, x.item_code, x.records, x.first_at, x.last_at
FROM raw_payloads r
JOIN connections c ON c.id = r.connection_id
CROSS JOIN (SELECT unnest(@origin_keys::text[]) AS origin_key, unnest(@item_kinds::text[]) AS item_kind,
  unnest(@item_codes::text[]) AS item_code, unnest(@records::integer[]) AS records,
  unnest(@first_ats::timestamptz[]) AS first_at, unnest(@last_ats::timestamptz[]) AS last_at) AS x
JOIN data_origins o ON o.user_id = r.user_id AND o.provider_id = c.provider_id AND o.origin_key = x.origin_key
WHERE r.id = @raw_payload_id;

-- name: ListIgnoredRawForTake :many
-- The device's raw payloads holding ignored records of these origins, to normalize again once
-- they are taken. type is the HealthKit type of the page (external_key is '<type>:<key>').
SELECT DISTINCT r.id, r.batch_id, o.origin_key, split_part(r.external_key, ':', 1)::text AS type
FROM ignored_records i
JOIN data_origins o ON o.id = i.origin_id
JOIN raw_payloads r ON r.id = i.raw_payload_id
JOIN ingest_batches b ON b.id = r.batch_id
WHERE i.user_id = @user_id AND b.client_id = @client_id AND o.origin_key = ANY(@origin_keys::text[])
ORDER BY r.id;

-- name: ListIgnoredItems :many
-- Explore's "ignored sources": records held raw per item and origin.
SELECT i.item_kind, i.item_code, o.origin_key, o.name AS origin_name, sum(i.records)::bigint AS records,
  min(i.first_at)::timestamptz AS first_at, max(i.last_at)::timestamptz AS last_at
FROM ignored_records i JOIN data_origins o ON o.id = i.origin_id
WHERE i.user_id = @user_id
GROUP BY 1, 2, 3, 4
ORDER BY 1, 2, 3;

-- name: ListIgnoredForMetric :many
-- The all-sources view's "ignored sources": records of a metric held raw in [start, end), per origin.
SELECT o.origin_key, o.name AS origin_name, sum(i.records)::bigint AS records,
  min(i.first_at)::timestamptz AS first_at, max(i.last_at)::timestamptz AS last_at
FROM ignored_records i JOIN data_origins o ON o.id = i.origin_id
WHERE i.user_id = @user_id AND i.item_kind = 'metric' AND i.item_code = @metric
  AND i.last_at >= @start::timestamptz AND i.first_at < @end_at::timestamptz
GROUP BY 1, 2
ORDER BY 1;

-- name: ListAppleRelayPatterns :many
-- Every known Apple Health relay origin, connected directly or not, to classify apps not seen yet.
SELECT k.origin_pattern, p.code AS provider
FROM known_relay_origins k
JOIN providers t ON t.id = k.provider_id AND t.code = 'apple_health'
JOIN providers p ON p.id = k.relayed_provider_id
ORDER BY 1;
