-- Withings records a phone app relayed (model ids 1051-1060, activity brand 18) carry an origin
-- relay:… (J25.2, docs/providers/withings.md#devices). phone_app is seeded only as their relay
-- target, so the origin is flagged relayed and never counted twice.

-- +goose Up
INSERT INTO providers (code, name) VALUES ('phone_app', 'Phone app') ON CONFLICT (code) DO NOTHING;

INSERT INTO known_relay_origins (provider_id, origin_pattern, relayed_provider_id)
SELECT t.id, 'relay:%', r.id
FROM providers t, providers r
WHERE t.code = 'withings' AND r.code = 'phone_app'
ON CONFLICT (provider_id, origin_pattern) DO NOTHING;

-- +goose Down
DELETE FROM known_relay_origins WHERE origin_pattern = 'relay:%'
  AND provider_id = (SELECT id FROM providers WHERE code = 'withings');
DELETE FROM providers WHERE code = 'phone_app';
