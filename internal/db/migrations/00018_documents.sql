-- Blood-test documents, extraction runs, review and confirmed lab results (J12.1;
-- docs/architecture/lab-documents.md). PDFs are blobs sealed with a per-document data key
-- (document_keys); deleting the key row crypto-shreds the PDF, its filename and every value
-- sealed with that key. The analyte foreign keys are added by the analytes seed migration.

-- +goose Up
CREATE TABLE documents (
  id                  uuid PRIMARY KEY,
  user_id             uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  status              text NOT NULL DEFAULT 'uploaded'
                        CHECK (status IN ('uploaded', 'extracting', 'needs_review', 'confirmed', 'deleted')),
  sha256              bytea CHECK (length(sha256) = 32),
  blob_sha256         bytea REFERENCES blobs,
  filename_ciphertext bytea,
  size_bytes          bigint NOT NULL CHECK (size_bytes > 0),
  page_count          integer NOT NULL CHECK (page_count > 0),
  uploaded_at         timestamptz NOT NULL DEFAULT now(),
  uploaded_by         text NOT NULL,
  retention_until     timestamptz,
  deleted_at          timestamptz,
  CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
  CHECK (status = 'deleted' OR (sha256 IS NOT NULL AND blob_sha256 IS NOT NULL)),
  CHECK (status <> 'deleted' OR (sha256 IS NULL AND blob_sha256 IS NULL AND filename_ciphertext IS NULL))
);
COMMENT ON TABLE documents IS 'Uploaded lab PDFs. A deleted document stays as a tombstone (no hash, blob or filename) so kept lab results still name their source.';
COMMENT ON COLUMN documents.sha256 IS 'SHA-256 of the PDF; a re-upload of the same content links to the live document.';
COMMENT ON COLUMN documents.blob_sha256 IS 'Blob holding the PDF sealed with the document key (internal/documents format), so its hash says nothing about the PDF.';
COMMENT ON COLUMN documents.filename_ciphertext IS 'Original filename sealed with the document key; null when none was given.';
COMMENT ON COLUMN documents.retention_until IS 'uploaded_at + documents.retention_days at the time of the last policy change; the document_retention job deletes the original after it.';
CREATE UNIQUE INDEX documents_user_sha256_idx ON documents (user_id, sha256) WHERE sha256 IS NOT NULL;
CREATE INDEX documents_user_idx ON documents (user_id, id);
CREATE INDEX documents_retention_idx ON documents (retention_until) WHERE status <> 'deleted';

CREATE TABLE document_keys (
  document_id uuid PRIMARY KEY REFERENCES documents ON DELETE CASCADE,
  ciphertext  bytea NOT NULL,
  key_id      text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE document_keys IS 'Per-document AES-256-GCM data key, sealed with the master key (purpose documents, AAD document_keys:<document id>). Deleting the row crypto-shreds the document.';
COMMENT ON COLUMN document_keys.key_id IS 'Master key id of ciphertext, for rotation.';

CREATE TABLE extraction_runs (
  id                   uuid PRIMARY KEY,
  document_id          uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
  user_id              uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  job_id               uuid REFERENCES jobs ON DELETE SET NULL,
  status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'confirmed')),
  provider             text NOT NULL,
  model                text,
  external             boolean NOT NULL,
  consent              jsonb,
  schema_version       text NOT NULL,
  prompt_version       text NOT NULL,
  provider_request_id  text,
  response_blob_sha256 bytea REFERENCES blobs,
  doc_meta             jsonb NOT NULL DEFAULT '{}',
  usage                jsonb NOT NULL DEFAULT '{}',
  warnings             text[] NOT NULL DEFAULT '{}',
  error_class          text,
  created_by           text NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  started_at           timestamptz,
  finished_at          timestamptz,
  CHECK (NOT external OR consent IS NOT NULL)
);
COMMENT ON TABLE extraction_runs IS 'One extraction attempt of a document. Re-extraction adds a run; runs can be compared. Deleted with the document original.';
COMMENT ON COLUMN extraction_runs.consent IS '{provider, model, acknowledged_at} the owner gave for an external provider.';
COMMENT ON COLUMN extraction_runs.response_blob_sha256 IS 'Raw provider response, sealed with the document key before Put.';
COMMENT ON COLUMN extraction_runs.doc_meta IS 'Document-level fields the extractor read (laboratory, dates).';
CREATE INDEX extraction_runs_document_idx ON extraction_runs (document_id, created_at);

CREATE TABLE lab_extracted_rows (
  id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_id                uuid NOT NULL REFERENCES extraction_runs ON DELETE CASCADE,
  row_index             integer NOT NULL CHECK (row_index >= 0),
  page                  integer CHECK (page > 0),
  analyte_label         text NOT NULL,
  value_text            text,
  value_numeric         double precision CHECK (value_numeric NOT IN ('NaN', 'Infinity', '-Infinity')),
  comparator            text CHECK (comparator IN ('<', '>', '<=', '>=')),
  unit_text             text,
  reference_range_text  text,
  ref_low               double precision,
  ref_high              double precision,
  abnormal_flag_printed text,
  specimen_type         text,
  collected_at          text,
  reported_at           text,
  laboratory            text,
  evidence_text         text,
  bbox                  jsonb,
  confidence            real CHECK (confidence BETWEEN 0 AND 1),
  warnings              text[] NOT NULL DEFAULT '{}',
  suggested_analyte_id  smallint,
  review_status         text NOT NULL DEFAULT 'pending' CHECK (review_status IN ('pending', 'accepted', 'edited', 'rejected')),
  reviewed_at           timestamptz,
  UNIQUE (run_id, row_index)
);
COMMENT ON TABLE lab_extracted_rows IS 'Extracted row schema v1 (lab-documents.md#extracted-row-schema-v1), verbatim from the extractor and edited in review.';
COMMENT ON COLUMN lab_extracted_rows.collected_at IS 'ISO 8601 local time without offset, as extracted; the owner''s timezone applies at confirmation.';
COMMENT ON COLUMN lab_extracted_rows.confidence IS 'Extractor hint only; never a reason to skip review.';
COMMENT ON COLUMN lab_extracted_rows.suggested_analyte_id IS 'From analyte_aliases; a suggestion until the owner confirms.';

CREATE TABLE extraction_row_edits (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  row_id     bigint NOT NULL REFERENCES lab_extracted_rows ON DELETE CASCADE,
  action     text NOT NULL CHECK (action IN ('edit', 'accept', 'reject')),
  changes    jsonb NOT NULL DEFAULT '{}',
  actor      text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE extraction_row_edits IS 'Append-only review trail of each extracted row.';
COMMENT ON COLUMN extraction_row_edits.changes IS '{"field": {"from": old, "to": new}} for edits.';
CREATE INDEX extraction_row_edits_row_idx ON extraction_row_edits (row_id, id);
REVOKE UPDATE ON extraction_row_edits FROM vitamux_app;

CREATE TABLE lab_reports (
  id             uuid PRIMARY KEY,
  user_id        uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  document_id    uuid NOT NULL REFERENCES documents ON DELETE CASCADE,
  run_id         uuid REFERENCES extraction_runs ON DELETE SET NULL,
  laboratory     text,
  reported_at    timestamptz,
  provider       text NOT NULL,
  model          text,
  schema_version text NOT NULL,
  prompt_version text NOT NULL,
  confirmed_by   text NOT NULL,
  confirmed_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE lab_reports IS 'A confirmed extraction. Extractor versions are copied from the run so they survive deleting the original.';
CREATE INDEX lab_reports_document_idx ON lab_reports (document_id);

CREATE TABLE lab_results (
  id                    uuid PRIMARY KEY,
  user_id               uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  report_id             uuid NOT NULL REFERENCES lab_reports ON DELETE CASCADE,
  source_row_id         bigint REFERENCES lab_extracted_rows ON DELETE SET NULL,
  analyte_id            smallint,
  original_label        text NOT NULL,
  value_text            text NOT NULL,
  value_numeric         double precision CHECK (value_numeric NOT IN ('NaN', 'Infinity', '-Infinity')),
  comparator            text CHECK (comparator IN ('<', '>', '<=', '>=')),
  unit_text             text,
  reference_range_text  text,
  ref_low               double precision,
  ref_high              double precision,
  abnormal_flag_printed text,
  specimen_type         text,
  canonical_value       double precision CHECK (canonical_value NOT IN ('NaN', 'Infinity', '-Infinity')),
  canonical_unit        text,
  conversion_factor     double precision,
  conversion_offset     double precision,
  catalog_version       integer,
  collected_at          timestamptz,
  collected_date        date NOT NULL,
  page                  integer CHECK (page > 0),
  evidence_text         text,
  revision              integer NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CHECK ((canonical_value IS NULL) = (canonical_unit IS NULL)),
  CHECK (canonical_value IS NULL OR (analyte_id IS NOT NULL AND value_numeric IS NOT NULL
    AND conversion_factor IS NOT NULL AND conversion_offset IS NOT NULL AND catalog_version IS NOT NULL))
);
COMMENT ON TABLE lab_results IS 'Confirmed lab values. The printed label, value text, unit, range and flag are always kept; no interpretation is stored.';
COMMENT ON COLUMN lab_results.analyte_id IS 'Null for an unknown analyte, which stays confirmable with its printed values.';
COMMENT ON COLUMN lab_results.unit_text IS 'Printed unit; null means the owner confirmed the value as unitless.';
COMMENT ON COLUMN lab_results.canonical_value IS 'Only when the analyte has a conversion for unit_text (internal/documents/analytes); never computed otherwise.';
COMMENT ON COLUMN lab_results.conversion_factor IS 'Factor and offset applied (canonical = value * factor + offset), so conventions such as insulin µIU/mL stay traceable.';
COMMENT ON COLUMN lab_results.collected_date IS 'Collection date in the owner''s timezone.';
CREATE INDEX lab_results_user_date_idx ON lab_results (user_id, collected_date, id);
CREATE INDEX lab_results_report_idx ON lab_results (report_id);

CREATE TABLE lab_result_revisions (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  result_id  uuid NOT NULL REFERENCES lab_results ON DELETE CASCADE,
  revision   integer NOT NULL CHECK (revision > 0),
  snapshot   jsonb NOT NULL,
  reason     text,
  changed_by text NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (result_id, revision)
);
COMMENT ON TABLE lab_result_revisions IS 'Append-only: the values of lab_results revision N before an edit made it N + 1.';
REVOKE UPDATE ON lab_result_revisions FROM vitamux_app;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check, ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('sync', 'backfill_unit',
  'normalize_batch', 'reprocess', 'rebuild_aggregates', 'extract_document', 'export', 'backup', 'import_unit',
  'prune_retention', 'sweep_blobs', 'recompute_local_dates', 'document_retention'));

-- +goose Down
DELETE FROM jobs WHERE kind = 'document_retention';
ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check, ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('sync', 'backfill_unit',
  'normalize_batch', 'reprocess', 'rebuild_aggregates', 'extract_document', 'export', 'backup', 'import_unit',
  'prune_retention', 'sweep_blobs', 'recompute_local_dates'));
DROP TABLE lab_result_revisions, lab_results, lab_reports, extraction_row_edits, lab_extracted_rows, extraction_runs,
  document_keys, documents;
