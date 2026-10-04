-- Relay targets and HealthKit origin ids for brands the suggested defaults rank (J15.1), see
-- docs/architecture/apple-health.md#brand-origin-sets. whoop, polar and fitbit are seeded as relay
-- targets, like garmin and oura in 00003; connectors for them add their own behaviour, not the rows.
-- Only bundle ids confirmed in the App Store are seeded. Ultrahuman, Eight Sleep, Strava and Zepp are not.

-- +goose Up
INSERT INTO providers (code, name) VALUES
  ('whoop', 'WHOOP'),
  ('polar', 'Polar'),
  ('fitbit', 'Fitbit')
ON CONFLICT (code) DO NOTHING;

INSERT INTO known_relay_origins (provider_id, origin_pattern, relayed_provider_id)
SELECT t.id, v.pattern, r.id
FROM (VALUES
  ('com.whoop.iphone', 'whoop'),
  ('fi.polar.polarflow', 'polar'),
  ('com.fitbit.FitbitMobile', 'fitbit')
) AS v (pattern, relayed)
JOIN providers t ON t.code = 'apple_health'
JOIN providers r ON r.code = v.relayed
ON CONFLICT (provider_id, origin_pattern) DO NOTHING;

-- +goose Down
DELETE FROM known_relay_origins
WHERE origin_pattern IN ('com.whoop.iphone', 'fi.polar.polarflow', 'com.fitbit.FitbitMobile');
DELETE FROM providers WHERE code IN ('whoop', 'polar', 'fitbit');
