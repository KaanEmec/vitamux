-- Raw payload versions (J05.3). A version is unique per (connection, stream, external key) and
-- points at the version it replaces. Content may repeat across versions (A → B → A is three
-- versions), so the content hash is no longer part of the unique key.

-- +goose Up
ALTER TABLE raw_payloads
  DROP CONSTRAINT raw_payloads_connection_id_stream_external_key_content_sha2_key,
  ADD COLUMN supersedes_id bigint REFERENCES raw_payloads,
  ADD CONSTRAINT raw_payloads_version_key UNIQUE (connection_id, stream, external_key, version),
  ADD CONSTRAINT raw_payloads_supersedes_check CHECK ((version = 1) = (supersedes_id IS NULL));
COMMENT ON COLUMN raw_payloads.supersedes_id IS 'Previous version of the same (connection, stream, external_key); null for version 1.';

-- +goose Down
ALTER TABLE raw_payloads
  DROP CONSTRAINT raw_payloads_supersedes_check,
  DROP CONSTRAINT raw_payloads_version_key,
  DROP COLUMN supersedes_id,
  ADD CONSTRAINT raw_payloads_connection_id_stream_external_key_content_sha2_key
    UNIQUE (connection_id, stream, external_key, content_sha256);
