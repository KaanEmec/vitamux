-- Portable exports and the NDJSON importer (J10.6; docs/architecture/api.md#exports).
-- An export's zip is a blob; its status is derived from the job while finished_at is null.
-- The download token is single use and short-lived; only its SHA-256 is stored.

-- +goose Up
CREATE TABLE exports (
  id               uuid PRIMARY KEY,
  user_id          uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  job_id           uuid REFERENCES jobs ON DELETE SET NULL,
  format           text NOT NULL CHECK (format IN ('ndjson', 'csv')),
  include_raw      boolean NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz,
  expires_at       timestamptz,
  size_bytes       bigint,
  blob_sha256      bytea REFERENCES blobs,
  token_hash       bytea,
  token_expires_at timestamptz,
  token_used_at    timestamptz,
  CHECK ((finished_at IS NULL) = (blob_sha256 IS NULL))
);
COMMENT ON TABLE exports IS 'Export zips (blob_sha256 holds a blob reference); deleted with their blob after expires_at.';
COMMENT ON COLUMN exports.token_hash IS 'SHA-256 of the current one-time download token; a new token replaces it.';

-- The importer keeps exported ids: it reserves an id range with setval before inserting with
-- OVERRIDING SYSTEM VALUE (internal/export/importer.go).
GRANT UPDATE ON SEQUENCE normalizer_versions_id_seq, raw_payloads_id_seq, import_items_id_seq,
  measurement_groups_id_seq, measurements_id_seq, sleep_stages_id_seq, audit_events_id_seq TO vitamux_app;

-- +goose Down
REVOKE UPDATE ON SEQUENCE normalizer_versions_id_seq, raw_payloads_id_seq, import_items_id_seq,
  measurement_groups_id_seq, measurements_id_seq, sleep_stages_id_seq, audit_events_id_seq FROM vitamux_app;
DROP TABLE exports;
