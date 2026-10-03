-- Resolved endpoints (J10.3, internal/api/resolved.go).

-- name: ResolvedSourceProvenance :many
-- Provenance of the all-sources drilldown: the rows of each source (src, 0-based, parallel to
-- ids) with their raw payloads, normalizers and the provider an origin relays.
SELECT k.src::integer AS src,
  COALESCE(array_agg(DISTINCT x.raw_payload_id ORDER BY x.raw_payload_id) FILTER (WHERE x.raw_payload_id IS NOT NULL), '{}')::bigint[] AS raw_payload_ids,
  string_agg(DISTINCT nv.name || '@' || nv.version::text, ', ')::text AS normalizer,
  COALESCE(max(rp.code), '')::text AS relayed_provider
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@srcs::integer[]) AS src) AS k
JOIN measurements x ON x.id = k.id AND x.user_id = @user_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN data_origins o ON o.id = x.origin_id
LEFT JOIN providers rp ON rp.id = o.relayed_provider_id
GROUP BY k.src
ORDER BY k.src;
