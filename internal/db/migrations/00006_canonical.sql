-- Canonical health data (J02.4); see docs/architecture/data-model.md#measurements.
-- Source rows are never updated in place: a change inserts a new row and supersedes the old one,
-- an upstream deletion sets deleted_at. "Active" means superseded_at IS NULL AND deleted_at IS NULL.
-- metric_catalog and units are seeded from internal/catalog (J07.1).

-- +goose Up
CREATE TABLE units (
  id   smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code text NOT NULL UNIQUE
);

CREATE TABLE metric_catalog (
  id      smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code    text NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_]*$'),
  unit_id smallint NOT NULL REFERENCES units
);
COMMENT ON TABLE metric_catalog IS 'Metrics owned by internal/catalog. Sources combine only when they share a code.';
REVOKE INSERT, UPDATE, DELETE ON units, metric_catalog FROM vitamux_app;

CREATE TABLE normalizer_versions (
  id            integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name          text NOT NULL,
  version       integer NOT NULL,
  git_sha       text NOT NULL,
  registered_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (name, version, git_sha)
);

CREATE TABLE measurement_groups (
  id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users,
  kind                  text NOT NULL CHECK (kind IN ('bp_reading', 'body_composition')),
  measured_at           timestamptz NOT NULL,
  tz_offset_min         smallint CHECK (tz_offset_min BETWEEN -1080 AND 1080),
  local_date            date NOT NULL,
  context               jsonb NOT NULL DEFAULT '{}',
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
  superseded_by         bigint REFERENCES measurement_groups,
  deleted_at            timestamptz,
  deleted_by_raw_id     bigint REFERENCES raw_payloads
);
COMMENT ON TABLE measurement_groups IS 'Readings taken together (blood pressure, weigh-ins); components are measurements with group_id.';
CREATE UNIQUE INDEX measurement_groups_dedupe_idx ON measurement_groups (dedupe_key) WHERE superseded_at IS NULL;
CREATE INDEX measurement_groups_user_kind_idx ON measurement_groups (user_id, kind, measured_at)
  WHERE superseded_at IS NULL AND deleted_at IS NULL;

CREATE TABLE measurements (
  id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users,
  metric_id             smallint NOT NULL REFERENCES metric_catalog,
  kind                  text NOT NULL CHECK (kind IN ('sample', 'interval', 'cumulative', 'daily_value')),
  start_at              timestamptz NOT NULL,
  end_at                timestamptz,
  tz_offset_min         smallint CHECK (tz_offset_min BETWEEN -1080 AND 1080),
  local_date            date NOT NULL,
  value                 double precision NOT NULL,
  source_value          double precision,
  source_unit_id        smallint REFERENCES units,
  provider_id           smallint NOT NULL REFERENCES providers,
  connection_id         uuid NOT NULL REFERENCES connections,
  device_id             uuid REFERENCES devices,
  origin_id             uuid REFERENCES data_origins,
  group_id              bigint REFERENCES measurement_groups,
  external_id           text,
  dedupe_key            bytea NOT NULL CHECK (length(dedupe_key) = 16),
  quality_flags         integer NOT NULL DEFAULT 0,
  raw_payload_id        bigint REFERENCES raw_payloads,
  normalizer_version_id integer NOT NULL REFERENCES normalizer_versions,
  ingested_at           timestamptz NOT NULL DEFAULT now(),
  normalized_at         timestamptz NOT NULL DEFAULT now(),
  superseded_at         timestamptz,
  superseded_by         bigint REFERENCES measurements,
  deleted_at            timestamptz,
  deleted_by_raw_id     bigint REFERENCES raw_payloads,
  CONSTRAINT measurements_end_check CHECK ((end_at IS NULL) = (kind = 'sample') AND end_at >= start_at),
  CONSTRAINT measurements_source_check CHECK ((source_value IS NULL) = (source_unit_id IS NULL))
);
COMMENT ON COLUMN measurements.value IS 'In the metric''s canonical unit.';
COMMENT ON COLUMN measurements.source_value IS 'Original value and unit, only when conversion changed the value.';
COMMENT ON COLUMN measurements.dedupe_key IS 'First 16 bytes of SHA-256 over the versioned key string (data-model.md#identifiers-and-dedupe-keys).';
COMMENT ON COLUMN measurements.quality_flags IS 'Bitset defined in code: manual_entry, motion_context, implausible, relayed, migrated_without_raw, prorated_source.';
COMMENT ON COLUMN measurements.raw_payload_id IS 'Null only for rows migrated without raw (quality flag).';
CREATE UNIQUE INDEX measurements_dedupe_idx ON measurements (dedupe_key) WHERE superseded_at IS NULL;
CREATE INDEX measurements_metric_start_idx ON measurements (user_id, metric_id, start_at)
  WHERE superseded_at IS NULL AND deleted_at IS NULL;
CREATE INDEX measurements_daily_value_idx ON measurements (user_id, metric_id, local_date)
  WHERE kind = 'daily_value' AND superseded_at IS NULL AND deleted_at IS NULL;
CREATE INDEX measurements_group_idx ON measurements (group_id) WHERE group_id IS NOT NULL;
-- Keep deletes cheap: the self and raw references below are rare, so their indexes stay small.
CREATE INDEX measurements_superseded_by_idx ON measurements (superseded_by) WHERE superseded_by IS NOT NULL;
CREATE INDEX measurements_deleted_by_raw_idx ON measurements (deleted_by_raw_id) WHERE deleted_by_raw_id IS NOT NULL;
CREATE INDEX measurements_raw_payload_brin ON measurements USING brin (raw_payload_id);
CREATE INDEX measurements_ingested_at_brin ON measurements USING brin (ingested_at);

CREATE TABLE sleep_sessions (
  id                    uuid PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users,
  start_at              timestamptz NOT NULL,
  end_at                timestamptz NOT NULL,
  tz_offset_min         smallint CHECK (tz_offset_min BETWEEN -1080 AND 1080),
  sleep_date            date NOT NULL,
  is_nap                boolean NOT NULL DEFAULT false,
  has_stages            boolean NOT NULL,
  totals_basis          text NOT NULL CHECK (totals_basis IN ('provider', 'stages')),
  asleep_s              integer,
  deep_s                integer,
  light_s               integer,
  rem_s                 integer,
  awake_s               integer,
  latency_s             integer,
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
  superseded_by         uuid REFERENCES sleep_sessions,
  deleted_at            timestamptz,
  deleted_by_raw_id     bigint REFERENCES raw_payloads,
  CHECK (end_at > start_at)
);
COMMENT ON COLUMN sleep_sessions.sleep_date IS 'Local date of waking up.';
COMMENT ON COLUMN sleep_sessions.totals_basis IS 'Whether the *_s totals are provider-reported or summed from stages.';
CREATE UNIQUE INDEX sleep_sessions_dedupe_idx ON sleep_sessions (dedupe_key) WHERE superseded_at IS NULL;
CREATE INDEX sleep_sessions_user_date_idx ON sleep_sessions (user_id, sleep_date)
  WHERE superseded_at IS NULL AND deleted_at IS NULL;

CREATE TABLE sleep_stages (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  session_id uuid NOT NULL REFERENCES sleep_sessions ON DELETE CASCADE,
  stage      text NOT NULL CHECK (stage IN ('awake', 'light', 'deep', 'rem', 'asleep_unspecified', 'in_bed')),
  start_at   timestamptz NOT NULL,
  end_at     timestamptz NOT NULL,
  CHECK (end_at > start_at)
);
CREATE INDEX sleep_stages_session_idx ON sleep_stages (session_id, start_at);

CREATE TABLE workouts (
  id                    uuid PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users,
  start_at              timestamptz NOT NULL,
  end_at                timestamptz NOT NULL,
  tz_offset_min         smallint CHECK (tz_offset_min BETWEEN -1080 AND 1080),
  local_date            date NOT NULL,
  sport                 text NOT NULL,
  provider_sport        text,
  distance_m            double precision,
  energy_kcal           double precision,
  avg_hr_bpm            double precision,
  max_hr_bpm            double precision,
  file_blob_sha256      bytea REFERENCES blobs,
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
  superseded_by         uuid REFERENCES workouts,
  deleted_at            timestamptz,
  deleted_by_raw_id     bigint REFERENCES raw_payloads,
  CHECK (end_at > start_at)
);
COMMENT ON COLUMN workouts.file_blob_sha256 IS 'Original activity file (FIT, GPX), if any.';
CREATE UNIQUE INDEX workouts_dedupe_idx ON workouts (dedupe_key) WHERE superseded_at IS NULL;
CREATE INDEX workouts_user_start_idx ON workouts (user_id, start_at) WHERE superseded_at IS NULL AND deleted_at IS NULL;

CREATE TABLE workout_segments (
  workout_id uuid NOT NULL REFERENCES workouts ON DELETE CASCADE,
  seq        integer NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('lap', 'set', 'interval')),
  start_at   timestamptz NOT NULL,
  end_at     timestamptz,
  data       jsonb NOT NULL DEFAULT '{}',
  PRIMARY KEY (workout_id, seq),
  CHECK (end_at >= start_at)
);

CREATE TABLE resolution_dirty (
  user_id    uuid NOT NULL REFERENCES users,
  metric_id  smallint NOT NULL REFERENCES metric_catalog,
  local_date date NOT NULL,
  marked_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, metric_id, local_date)
);
COMMENT ON TABLE resolution_dirty IS 'Days whose resolved values must be recomputed; written in the canonical writer''s transaction.';

-- +goose Down
DROP TABLE resolution_dirty, workout_segments, workouts, sleep_stages, sleep_sessions, measurements,
  measurement_groups, normalizer_versions, metric_catalog, units;
