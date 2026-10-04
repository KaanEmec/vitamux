-- Device pairing (J15.2); see docs/architecture/apple-health.md#pairing-and-security.

-- +goose Up
CREATE TABLE pairing_codes (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  code_hash  bytea NOT NULL UNIQUE CHECK (length(code_hash) = 32),
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);
COMMENT ON TABLE pairing_codes IS 'Short-lived, single-use codes a device exchanges for a client token at POST /api/ingest/v1/devices/pair. Only the code''s SHA-256 is stored.';
CREATE INDEX pairing_codes_user_created_idx ON pairing_codes (user_id, created_at);

ALTER TABLE clients ADD COLUMN anchor_resets jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(anchor_resets) = 'object');
COMMENT ON COLUMN clients.anchor_resets IS 'Device anchor resets the owner requested: HealthKit type identifier (or * for every type) to the latest request time. The device applies those newer than the last it applied.';

-- +goose Down
ALTER TABLE clients DROP COLUMN anchor_resets;
DROP TABLE pairing_codes;
