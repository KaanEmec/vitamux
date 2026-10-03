-- Source registry (J02.3): providers, connections, credentials, clients, devices, origins.

-- +goose Up
CREATE TABLE providers (
  id   smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code text NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_]*$'),
  name text NOT NULL
);
COMMENT ON TABLE providers IS 'Data vendors and transports. Seeded by migrations; read-only for the app role.';
-- garmin and oura are seeded only as relay targets for known_relay_origins.
INSERT INTO providers (code, name) VALUES
  ('withings', 'Withings'),
  ('apple_health', 'Apple Health'),
  ('manual', 'Manual entry'),
  ('file_import', 'File import'),
  ('lab_document', 'Lab document'),
  ('garmin', 'Garmin'),
  ('oura', 'Oura');
REVOKE INSERT, UPDATE, DELETE ON providers FROM vitamux_app;

CREATE TABLE connections (
  id                   uuid PRIMARY KEY,
  user_id              uuid NOT NULL REFERENCES users,
  provider_id          smallint NOT NULL REFERENCES providers,
  account_key          bytea CHECK (length(account_key) = 32),
  mode                 text NOT NULL CHECK (mode IN ('in_process', 'push', 'remote')),
  status               text NOT NULL CHECK (status IN ('active', 'degraded', 'needs_reauth', 'paused', 'error', 'disabled')),
  config               jsonb NOT NULL DEFAULT '{}',
  last_success_at      timestamptz,
  last_error_class     text,
  consecutive_failures integer NOT NULL DEFAULT 0,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, provider_id, account_key)
);
COMMENT ON COLUMN connections.account_key IS 'SHA-256 of the provider account id; null until the account is known.';

CREATE TABLE credentials (
  connection_id     uuid PRIMARY KEY REFERENCES connections ON DELETE CASCADE,
  ciphertext        bytea NOT NULL,
  key_id            text NOT NULL,
  access_expires_at timestamptz,
  version           integer NOT NULL DEFAULT 1,
  updated_at        timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE credentials IS 'Provider tokens, sealed by internal/crypto (purpose credentials, AAD bound to the connection).';
COMMENT ON COLUMN credentials.version IS 'Incremented on every refresh; guards the single-flight refresh.';

CREATE TABLE clients (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES users,
  connection_id uuid NOT NULL REFERENCES connections,
  kind          text NOT NULL CHECK (kind IN ('collector', 'device', 'importer')),
  name          text NOT NULL,
  token_hash    bytea NOT NULL,
  metadata      jsonb NOT NULL DEFAULT '{}',
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz,
  revoked_at    timestamptz
);
COMMENT ON TABLE clients IS 'Ingest tokens `vmx_cli_<id>_<secret>` scoped to one connection; only the secret''s SHA-256 is stored.';

CREATE TABLE devices (
  id               uuid PRIMARY KEY,
  user_id          uuid NOT NULL REFERENCES users,
  provider_id      smallint NOT NULL REFERENCES providers,
  fingerprint      text NOT NULL,
  device_type      text,
  manufacturer     text,
  model            text,
  hardware_version text,
  software_version text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, provider_id, fingerprint)
);
COMMENT ON COLUMN devices.device_type IS 'Rule selector, e.g. watch, phone, scale, bp_monitor, ring.';

CREATE TABLE data_origins (
  id                  uuid PRIMARY KEY,
  user_id             uuid NOT NULL REFERENCES users,
  provider_id         smallint NOT NULL REFERENCES providers,
  origin_key          text NOT NULL,
  name                text,
  relayed_provider_id smallint REFERENCES providers,
  is_native           boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, provider_id, origin_key)
);
COMMENT ON TABLE data_origins IS 'Apps that recorded data inside a transport provider, e.g. HealthKit bundle ids.';
COMMENT ON COLUMN data_origins.relayed_provider_id IS 'Set when the origin relays another vendor''s data (e.g. Garmin Connect into HealthKit).';

CREATE TABLE known_relay_origins (
  id                  smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  provider_id         smallint NOT NULL REFERENCES providers,
  origin_pattern      text NOT NULL,
  relayed_provider_id smallint NOT NULL REFERENCES providers,
  UNIQUE (provider_id, origin_pattern)
);
COMMENT ON TABLE known_relay_origins IS 'Editable defaults for data_origins.relayed_provider_id.';
COMMENT ON COLUMN known_relay_origins.origin_pattern IS 'LIKE pattern matched against data_origins.origin_key.';
INSERT INTO known_relay_origins (provider_id, origin_pattern, relayed_provider_id)
SELECT t.id, v.pattern, r.id
FROM (VALUES
  ('com.garmin.connect.mobile', 'garmin'),
  ('com.withings.wiScaleNG', 'withings'),
  ('com.ouraring.oura', 'oura')
) AS v (pattern, relayed)
JOIN providers t ON t.code = 'apple_health'
JOIN providers r ON r.code = v.relayed;

-- +goose Down
DROP TABLE known_relay_origins, data_origins, devices, clients, credentials, connections, providers;
