-- Extraction runs (J12.3); see docs/architecture/lab-documents.md#extraction-provider-interface.

-- name: InsertExtractionRun :exec
INSERT INTO extraction_runs (id, document_id, user_id, job_id, provider, model, external, consent, schema_version, prompt_version, created_by)
VALUES (@id, @document_id, @user_id, @job_id, @provider, sqlc.narg(model), @external, sqlc.narg(consent), @schema_version, @prompt_version, @created_by);

-- name: AbandonExtractionRuns :exec
-- Called under the document lock once no extraction job is active: a run still queued or
-- running lost its job (e.g. reaped after a crash on its last attempt).
UPDATE extraction_runs SET status = 'failed', error_class = 'abandoned', finished_at = now()
WHERE document_id = @document_id AND status IN ('queued', 'running');

-- name: GetExtractionRun :one
SELECT * FROM extraction_runs WHERE id = @id;

-- name: ListExtractionRuns :many
-- Newest first, with the number of extracted rows. Never the response blob.
SELECT r.id, r.document_id, r.status, r.provider, r.model, r.external, r.consent, r.schema_version, r.prompt_version,
       r.provider_request_id, r.doc_meta, r.usage, r.warnings, r.error_class, r.created_by, r.created_at, r.started_at,
       r.finished_at, (SELECT count(*) FROM lab_extracted_rows x WHERE x.run_id = r.id) AS row_count
FROM extraction_runs r
WHERE r.user_id = @user_id AND r.document_id = @document_id
ORDER BY r.created_at DESC, r.id DESC;

-- name: StartExtractionRun :execrows
UPDATE extraction_runs SET status = 'running', started_at = coalesce(started_at, now())
WHERE id = @id AND status IN ('queued', 'running');

-- name: RequeueExtractionRun :exec
-- The attempt failed and the job retries; error_class says why.
UPDATE extraction_runs SET status = 'queued', error_class = @error_class WHERE id = @id AND status IN ('queued', 'running');

-- name: FinishExtractionRun :exec
-- status is succeeded or failed; a failed run may still keep the (sealed) response it got.
UPDATE extraction_runs
SET status = @status, model = coalesce(sqlc.narg(model), model), provider_request_id = sqlc.narg(provider_request_id),
    response_blob_sha256 = sqlc.narg(response_blob_sha256), doc_meta = @doc_meta, usage = @usage, warnings = @warnings,
    error_class = sqlc.narg(error_class), finished_at = now()
WHERE id = @id;

-- name: InsertExtractedRow :exec
INSERT INTO lab_extracted_rows (run_id, row_index, page, analyte_label, value_text, value_numeric, comparator, unit_text,
  reference_range_text, ref_low, ref_high, abnormal_flag_printed, specimen_type, collected_at, reported_at, laboratory,
  evidence_text, bbox, confidence, warnings)
VALUES (@run_id, @row_index, sqlc.narg(page), @analyte_label, sqlc.narg(value_text), sqlc.narg(value_numeric), sqlc.narg(comparator),
  sqlc.narg(unit_text), sqlc.narg(reference_range_text), sqlc.narg(ref_low), sqlc.narg(ref_high), sqlc.narg(abnormal_flag_printed),
  sqlc.narg(specimen_type), sqlc.narg(collected_at), sqlc.narg(reported_at), sqlc.narg(laboratory), sqlc.narg(evidence_text),
  sqlc.narg(bbox), sqlc.narg(confidence), @warnings);

-- name: SetDocumentStatus :exec
-- Moves a live document between uploaded, extracting and needs_review; confirmed and deleted stay.
UPDATE documents SET status = @status WHERE id = @id AND status IN ('uploaded', 'extracting', 'needs_review');

-- name: HasSucceededExtraction :one
SELECT EXISTS (SELECT 1 FROM extraction_runs WHERE document_id = @document_id AND status IN ('succeeded', 'confirmed'));
