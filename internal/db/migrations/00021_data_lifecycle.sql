-- Data lifecycle (J13.4); see docs/architecture/data-model.md#retention.
-- prune_raw deletes the oldest versions of a raw record first, so a later version may lose its
-- predecessor: only version 1 is still required to have none. The never-used prune_retention
-- kind is replaced by one kind per retention job.

-- +goose Up
ALTER TABLE raw_payloads DROP CONSTRAINT raw_payloads_supersedes_check,
  ADD CONSTRAINT raw_payloads_supersedes_check CHECK (version > 1 OR supersedes_id IS NULL);
COMMENT ON COLUMN raw_payloads.supersedes_id IS 'Previous version of the same (connection, stream, external_key); null for version 1, and for a later version whose predecessor prune_raw deleted.';

-- prune_superseded walks superseded rows by age.
CREATE INDEX measurements_superseded_at_idx ON measurements (superseded_at) WHERE superseded_at IS NOT NULL;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check, ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('sync', 'backfill_unit',
  'normalize_batch', 'reprocess', 'rebuild_aggregates', 'extract_document', 'export', 'backup', 'import_unit',
  'sweep_blobs', 'recompute_local_dates', 'document_retention', 'prune_raw', 'prune_superseded', 'prune_idempotency_keys'));

-- +goose Down
DELETE FROM jobs WHERE kind IN ('prune_raw', 'prune_superseded', 'prune_idempotency_keys');
ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check, ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('sync', 'backfill_unit',
  'normalize_batch', 'reprocess', 'rebuild_aggregates', 'extract_document', 'export', 'backup', 'import_unit',
  'prune_retention', 'sweep_blobs', 'recompute_local_dates', 'document_retention'));
DROP INDEX measurements_superseded_at_idx;
-- NOT VALID: versions whose predecessor was pruned stay as they are.
ALTER TABLE raw_payloads DROP CONSTRAINT raw_payloads_supersedes_check,
  ADD CONSTRAINT raw_payloads_supersedes_check CHECK ((version = 1) = (supersedes_id IS NULL)) NOT VALID;
COMMENT ON COLUMN raw_payloads.supersedes_id IS 'Previous version of the same (connection, stream, external_key); null for version 1.';
