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
SELECT d.id, p.code AS provider, d.device_type, d.manufacturer, d.model
FROM devices d JOIN providers p ON p.id = d.provider_id
WHERE d.user_id = @user_id
ORDER BY p.code, d.device_type, d.model, d.id;
