-- Raw provenance and importers (J02.4). Raw rows are immutable; a changed upstream record is a new version.

-- +goose Up
CREATE TABLE blobs (
  sha256       bytea PRIMARY KEY CHECK (length(sha256) = 32),
  size_bytes   bigint NOT NULL CHECK (size_bytes >= 0),
  stored_bytes bigint NOT NULL CHECK (stored_bytes >= 0),
  compression  text NOT NULL CHECK (compression IN ('none', 'zstd')),
  key_id       text,
  refcount     integer NOT NULL DEFAULT 0 CHECK (refcount >= 0),
  created_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE blobs IS 'Content-addressed files in the blob volume, keyed by the SHA-256 of the uncompressed content.';
COMMENT ON COLUMN blobs.key_id IS 'Encryption key reference; null when the blob is not app-encrypted.';

CREATE TABLE ingest_batches (
  id               uuid PRIMARY KEY,
  user_id          uuid NOT NULL REFERENCES users,
  connection_id    uuid NOT NULL REFERENCES connections,
  client_id        uuid REFERENCES clients,
  source_kind      text NOT NULL CHECK (source_kind IN ('sync', 'push', 'import', 'manual')),
  migration_source text,
  idempotency_key  text,
  received_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (client_id, idempotency_key)
);
COMMENT ON COLUMN ingest_batches.client_id IS 'Pushing client; null for in-process syncs.';

CREATE TABLE raw_payloads (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id           uuid NOT NULL REFERENCES users,
  connection_id     uuid NOT NULL REFERENCES connections,
  batch_id          uuid NOT NULL REFERENCES ingest_batches,
  stream            text NOT NULL,
  external_key      text NOT NULL,
  version           integer NOT NULL DEFAULT 1 CHECK (version > 0),
  content_sha256    bytea NOT NULL REFERENCES blobs,
  content_type      text NOT NULL,
  fetched_at        timestamptz NOT NULL,
  stored_at         timestamptz NOT NULL DEFAULT now(),
  request_meta      jsonb NOT NULL DEFAULT '{}',
  shape_fingerprint text,
  status            text NOT NULL DEFAULT 'stored'
                      CHECK (status IN ('stored', 'normalized', 'normalize_failed', 'quarantined')),
  UNIQUE (connection_id, stream, external_key, content_sha256)
);
COMMENT ON COLUMN raw_payloads.content_sha256 IS 'Content hash and blob key: the verbatim payload is the blob.';
COMMENT ON COLUMN raw_payloads.request_meta IS 'Sanitized request (endpoint, params); never tokens.';
CREATE INDEX raw_payloads_batch_idx ON raw_payloads (batch_id);

CREATE TABLE import_runs (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES users,
  connection_id uuid REFERENCES connections,
  source        text NOT NULL,
  status        text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed', 'cancelled')),
  file_sha256   bytea,
  stats         jsonb NOT NULL DEFAULT '{}',
  started_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz
);
COMMENT ON COLUMN import_runs.source IS 'Importer, e.g. apple_health_export, ndjson.';

CREATE TABLE import_items (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  import_run_id  uuid NOT NULL REFERENCES import_runs ON DELETE CASCADE,
  source         text NOT NULL,
  item_key       text NOT NULL,
  checksum       bytea NOT NULL,
  status         text NOT NULL CHECK (status IN ('done', 'failed')),
  raw_payload_id bigint REFERENCES raw_payloads,
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source, item_key, checksum)
);
COMMENT ON TABLE import_items IS 'Items already imported, so re-running an importer skips them.';
CREATE INDEX import_items_run_idx ON import_items (import_run_id);

-- +goose Down
DROP TABLE import_items, import_runs, raw_payloads, ingest_batches, blobs;
