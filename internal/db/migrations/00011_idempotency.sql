-- Idempotency-Key store for ingest POSTs (J05.4); see docs/architecture/api.md#conventions.

-- +goose Up
CREATE TABLE idempotency_keys (
  client_id       uuid NOT NULL REFERENCES clients ON DELETE CASCADE,
  key             text NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  request_sha256  bytea NOT NULL CHECK (length(request_sha256) = 32),
  response_status smallint,
  response_body   jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (client_id, key)
);
COMMENT ON TABLE idempotency_keys IS 'First successful response per (client, Idempotency-Key), replayed for the same request; another request with the key is a conflict.';
COMMENT ON COLUMN idempotency_keys.request_sha256 IS 'SHA-256 over the route and the decompressed request body.';
COMMENT ON COLUMN idempotency_keys.response_status IS 'Null only inside the claiming transaction, so never in a committed row.';
COMMENT ON COLUMN idempotency_keys.response_body IS 'Batch or blob receipt: ids, external keys and outcomes; never payload bodies.';

-- +goose Down
DROP TABLE idempotency_keys;
