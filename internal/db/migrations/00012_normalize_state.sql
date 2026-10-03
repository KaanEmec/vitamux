-- Normalization outcome per raw payload (J07.5): which normalizer version produced the canonical
-- rows, why a payload failed, and the warnings it raised. Reprocess selects on the version.

-- +goose Up
ALTER TABLE raw_payloads
  ADD COLUMN normalizer_version_id integer REFERENCES normalizer_versions,
  ADD COLUMN normalized_at timestamptz,
  ADD COLUMN status_detail text,
  ADD COLUMN warnings jsonb NOT NULL DEFAULT '[]';
COMMENT ON COLUMN raw_payloads.normalizer_version_id IS 'Normalizer version of the last normalization attempt, failed ones included; null until one ran.';
COMMENT ON COLUMN raw_payloads.normalized_at IS 'When the last attempt finished.';
COMMENT ON COLUMN raw_payloads.status_detail IS 'Short code and reason for normalize_failed (no_normalizer, normalizer_error, normalizer_panic, invalid_output) or a skip (superseded_raw). Never payload content.';
COMMENT ON COLUMN raw_payloads.warnings IS 'Non-fatal findings of the last attempt: [{code, detail}]. Never health values.';
-- "Is this payload the newest version?" is asked per candidate by reprocess.
CREATE INDEX raw_payloads_supersedes_idx ON raw_payloads (supersedes_id) WHERE supersedes_id IS NOT NULL;

-- +goose Down
DROP INDEX raw_payloads_supersedes_idx;
ALTER TABLE raw_payloads
  DROP COLUMN warnings,
  DROP COLUMN status_detail,
  DROP COLUMN normalized_at,
  DROP COLUMN normalizer_version_id;
