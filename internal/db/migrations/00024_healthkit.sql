-- Health events (J15.2): typed events with a level or value, e.g. HealthKit heart-rate, sleep
-- apnea, hypertension, walking-steadiness and audio-exposure alerts (metric-catalog.md#events).
-- One table for every event family; history semantics as in 00006_canonical.sql. The E15 metric
-- codes are seeded by the generated 00025_catalogue_healthkit.sql.

-- +goose Up
CREATE TABLE health_events (
  id                    uuid PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users,
  code                  text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]*$'),
  start_at              timestamptz NOT NULL,
  end_at                timestamptz,
  tz_offset_min         smallint CHECK (tz_offset_min BETWEEN -1080 AND 1080),
  local_date            date NOT NULL,
  value                 double precision,
  level                 text,
  context               jsonb NOT NULL DEFAULT '{}',
  quality_flags         integer NOT NULL DEFAULT 0,
  provider_id           smallint NOT NULL REFERENCES providers,
  connection_id         uuid NOT NULL REFERENCES connections,
  device_id             uuid REFERENCES devices,
  origin_id             uuid REFERENCES data_origins,
  external_id           text,
  dedupe_key            bytea NOT NULL CHECK (length(dedupe_key) = 16),
  raw_payload_id        bigint REFERENCES raw_payloads,
  normalizer_version_id integer NOT NULL REFERENCES normalizer_versions,
  ingested_at           timestamptz NOT NULL DEFAULT now(),
  normalized_at         timestamptz NOT NULL DEFAULT now(),
  superseded_at         timestamptz,
  superseded_by         uuid REFERENCES health_events,
  deleted_at            timestamptz,
  deleted_by_raw_id     bigint REFERENCES raw_payloads,
  CHECK (end_at >= start_at)
);
COMMENT ON TABLE health_events IS 'Typed events (alerts, results) with a level or value; codes are owned by internal/catalog (Events).';
COMMENT ON COLUMN health_events.level IS 'Provider level mapped to a code-owned word, e.g. initial_low; null when the event has none.';
COMMENT ON COLUMN health_events.context IS 'Source metadata kept as given, e.g. HealthKit thresholds.';
COMMENT ON COLUMN health_events.quality_flags IS 'measurements.quality_flags bitset (manual_entry, relayed).';
CREATE UNIQUE INDEX health_events_dedupe_idx ON health_events (dedupe_key) WHERE superseded_at IS NULL;
CREATE INDEX health_events_user_code_idx ON health_events (user_id, code, start_at)
  WHERE superseded_at IS NULL AND deleted_at IS NULL;

-- +goose Down
DROP TABLE health_events;
