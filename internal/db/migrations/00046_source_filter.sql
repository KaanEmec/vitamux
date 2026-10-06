-- Apple Health source filter (J22.25, docs/architecture/apple-health.md#source-filter): per paired
-- device (a client of kind device), which origin apps' data the phone takes from Apple Health.
-- Only the owner's explicit choices are stored; the defaults (Apple's sources and unknown apps
-- take, a known relay origin of a directly connected provider ignore) are evaluated on every read,
-- so connecting or deleting a direct connection changes them without touching an explicit choice.
-- Rows that still arrive from an ignored origin are kept raw and listed in ignored_records
-- (ignored_by_filter) instead of being normalized.

-- +goose Up
ALTER TABLE clients
  ADD COLUMN source_filter jsonb NOT NULL DEFAULT '{"origins": []}' CHECK (jsonb_typeof(source_filter) = 'object'),
  ADD COLUMN source_filter_version integer NOT NULL DEFAULT 1 CHECK (source_filter_version > 0),
  ADD COLUMN health_sources jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(health_sources) = 'array'),
  ADD COLUMN health_sources_at timestamptz;
COMMENT ON COLUMN clients.source_filter IS 'Device source filter: the owner''s explicit choices {"origins": [{"bundle_id", "name", "mode": take|ignore|per_type, "types"}]}; per_type takes only the listed HealthKit types.';
COMMENT ON COLUMN clients.source_filter_version IS 'Incremented on every change of source_filter; the device and PUT preconditions read it.';
COMMENT ON COLUMN clients.health_sources IS 'The apps the device last found in Apple Health: [{"bundle_id", "name", "types": [{"type", "last_sample_at"}]}]. No health values.';
COMMENT ON COLUMN clients.health_sources_at IS 'When the device last reported health_sources.';

CREATE TABLE ignored_records (
  raw_payload_id bigint NOT NULL REFERENCES raw_payloads ON DELETE CASCADE,
  origin_id      uuid NOT NULL REFERENCES data_origins ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users,
  item_kind      text NOT NULL CHECK (item_kind IN ('metric', 'group', 'event', 'sleep', 'workouts')),
  item_code      text NOT NULL,
  reason         text NOT NULL DEFAULT 'ignored_by_filter' CHECK (reason = 'ignored_by_filter'),
  records        integer NOT NULL CHECK (records > 0),
  first_at       timestamptz NOT NULL,
  last_at        timestamptz NOT NULL,
  PRIMARY KEY (raw_payload_id, origin_id, item_kind, item_code)
);
COMMENT ON TABLE ignored_records IS 'Records of a raw payload that were not normalized because the device''s source filter ignores their origin (ignored_by_filter). The raw payload keeps them; normalizing it again after a take rewrites these rows.';
COMMENT ON COLUMN ignored_records.item_kind IS 'What the records would have been, as in the inventory: metric, group, event, sleep or workouts; item_code is the metric, group kind or event code.';
CREATE INDEX ignored_records_user_idx ON ignored_records (user_id, item_kind, item_code);
CREATE INDEX ignored_records_origin_idx ON ignored_records (origin_id);

-- +goose Down
DROP TABLE ignored_records;
ALTER TABLE clients DROP COLUMN health_sources_at, DROP COLUMN health_sources, DROP COLUMN source_filter_version, DROP COLUMN source_filter;
