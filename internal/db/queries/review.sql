-- Extraction review, confirmation and lab results (J12.4); see docs/architecture/lab-documents.md#review-and-confirmation.

-- name: GetOwnerExtractionRun :one
SELECT * FROM extraction_runs WHERE id = @id AND user_id = @user_id;

-- name: ListReviewRows :many
-- The rows of a run with the analyte code stored at review and the confirmed result, if any.
SELECT x.*, a.code AS analyte_code, r.id AS result_id
FROM lab_extracted_rows x
LEFT JOIN analytes a ON a.id = x.suggested_analyte_id
LEFT JOIN lab_results r ON r.source_row_id = x.id
WHERE x.run_id = @run_id
ORDER BY x.row_index;

-- name: ListRunRowEdits :many
SELECT e.* FROM extraction_row_edits e JOIN lab_extracted_rows x ON x.id = e.row_id
WHERE x.run_id = @run_id
ORDER BY e.id;

-- name: LockExtractedRow :one
SELECT x.*, a.code AS analyte_code FROM lab_extracted_rows x LEFT JOIN analytes a ON a.id = x.suggested_analyte_id
WHERE x.run_id = @run_id AND x.row_index = @row_index
FOR UPDATE OF x;

-- name: ReviewExtractedRow :exec
-- Writes the reviewed values; suggested_analyte_id becomes the analyte the owner reviewed.
UPDATE lab_extracted_rows
SET analyte_label = @analyte_label, value_text = sqlc.narg(value_text), value_numeric = sqlc.narg(value_numeric),
    comparator = sqlc.narg(comparator), unit_text = sqlc.narg(unit_text), reference_range_text = sqlc.narg(reference_range_text),
    ref_low = sqlc.narg(ref_low), ref_high = sqlc.narg(ref_high), abnormal_flag_printed = sqlc.narg(abnormal_flag_printed),
    specimen_type = sqlc.narg(specimen_type), collected_at = sqlc.narg(collected_at), reported_at = sqlc.narg(reported_at),
    laboratory = sqlc.narg(laboratory),
    suggested_analyte_id = (SELECT an.id FROM analytes an WHERE an.code = sqlc.narg(analyte)::text),
    review_status = @review_status, reviewed_at = now()
WHERE lab_extracted_rows.id = @id;

-- name: InsertRowEdit :exec
INSERT INTO extraction_row_edits (row_id, action, changes, actor) VALUES (@row_id, @action, @changes, @actor);

-- name: GetDocumentReport :one
-- The confirmed report of a document (one per document).
SELECT * FROM lab_reports WHERE document_id = @document_id ORDER BY confirmed_at LIMIT 1;

-- name: InsertLabReport :exec
INSERT INTO lab_reports (id, user_id, document_id, run_id, laboratory, reported_at, provider, model, schema_version, prompt_version, confirmed_by)
VALUES (@id, @user_id, @document_id, @run_id, sqlc.narg(laboratory), sqlc.narg(reported_at), @provider, sqlc.narg(model), @schema_version,
  @prompt_version, @confirmed_by);

-- name: DeleteLabReport :exec
DELETE FROM lab_reports WHERE id = @id;

-- name: ListReportResults :many
SELECT r.*, a.code AS analyte_code FROM lab_results r LEFT JOIN analytes a ON a.id = r.analyte_id
WHERE r.report_id = @report_id ORDER BY r.id;

-- name: InsertLabResult :exec
INSERT INTO lab_results (id, user_id, report_id, source_row_id, analyte_id, original_label, value_text, value_numeric, comparator,
  unit_text, reference_range_text, ref_low, ref_high, abnormal_flag_printed, specimen_type, canonical_value, canonical_unit,
  conversion_factor, conversion_offset, catalog_version, collected_at, collected_date, page, evidence_text)
VALUES (@id, @user_id, @report_id, @source_row_id, sqlc.narg(analyte_id), @original_label, @value_text, sqlc.narg(value_numeric),
  sqlc.narg(comparator), sqlc.narg(unit_text), sqlc.narg(reference_range_text), sqlc.narg(ref_low), sqlc.narg(ref_high),
  sqlc.narg(abnormal_flag_printed), sqlc.narg(specimen_type), sqlc.narg(canonical_value), sqlc.narg(canonical_unit),
  sqlc.narg(conversion_factor), sqlc.narg(conversion_offset), sqlc.narg(catalog_version), sqlc.narg(collected_at), @collected_date,
  sqlc.narg(page), sqlc.narg(evidence_text));

-- name: ReviseLabResult :exec
-- The caller stored the previous values in lab_result_revisions first.
UPDATE lab_results
SET analyte_id = sqlc.narg(analyte_id), original_label = @original_label, value_text = @value_text, value_numeric = sqlc.narg(value_numeric),
    comparator = sqlc.narg(comparator), unit_text = sqlc.narg(unit_text), reference_range_text = sqlc.narg(reference_range_text),
    ref_low = sqlc.narg(ref_low), ref_high = sqlc.narg(ref_high), abnormal_flag_printed = sqlc.narg(abnormal_flag_printed),
    specimen_type = sqlc.narg(specimen_type), canonical_value = sqlc.narg(canonical_value), canonical_unit = sqlc.narg(canonical_unit),
    conversion_factor = sqlc.narg(conversion_factor), conversion_offset = sqlc.narg(conversion_offset),
    catalog_version = sqlc.narg(catalog_version), collected_at = sqlc.narg(collected_at), collected_date = @collected_date,
    page = sqlc.narg(page), evidence_text = sqlc.narg(evidence_text), revision = revision + 1, updated_at = now()
WHERE id = @id;

-- name: InsertLabResultRevision :exec
INSERT INTO lab_result_revisions (result_id, revision, snapshot, reason, changed_by)
VALUES (@result_id, @revision, @snapshot, sqlc.narg(reason), @changed_by);

-- name: DeleteLabResult :exec
DELETE FROM lab_results WHERE id = @id;

-- name: SetExtractionRunStatus :exec
UPDATE extraction_runs SET status = @status WHERE id = @id;

-- name: SetLiveDocumentStatus :exec
-- Confirmation moves a live document to confirmed and back to needs_review.
UPDATE documents SET status = @status WHERE id = @id AND status <> 'deleted';

-- name: ListConfirmedAnalyteDates :many
-- Analytes and collection dates already confirmed from other runs, for duplicate warnings.
SELECT DISTINCT a.code, r.collected_date
FROM lab_results r JOIN lab_reports p ON p.id = r.report_id JOIN analytes a ON a.id = r.analyte_id
WHERE r.user_id = @user_id AND a.code = ANY(@codes::text[])
  AND p.run_id IS DISTINCT FROM @run_id::uuid;

-- name: ListLabResults :many
-- Keyset page by (collected_date, id) with the provenance of each result.
SELECT r.*, a.code AS analyte_code, p.document_id, p.run_id, p.laboratory, p.reported_at, p.provider, p.model,
       p.schema_version, p.prompt_version, p.confirmed_by, p.confirmed_at, x.row_index
FROM lab_results r
JOIN lab_reports p ON p.id = r.report_id
LEFT JOIN analytes a ON a.id = r.analyte_id
LEFT JOIN lab_extracted_rows x ON x.id = r.source_row_id
WHERE r.user_id = @user_id
  AND (sqlc.narg(start_date)::date IS NULL OR r.collected_date >= sqlc.narg(start_date)::date)
  AND (sqlc.narg(end_date)::date IS NULL OR r.collected_date <= sqlc.narg(end_date)::date)
  AND (sqlc.narg(after_date)::date IS NULL OR (r.collected_date, r.id) > (sqlc.narg(after_date)::date, @after_id::uuid))
ORDER BY r.collected_date, r.id
LIMIT @lim;

-- name: GetLabResult :one
SELECT r.*, a.code AS analyte_code, p.document_id, p.run_id, p.laboratory, p.reported_at, p.provider, p.model,
       p.schema_version, p.prompt_version, p.confirmed_by, p.confirmed_at, x.row_index
FROM lab_results r
JOIN lab_reports p ON p.id = r.report_id
LEFT JOIN analytes a ON a.id = r.analyte_id
LEFT JOIN lab_extracted_rows x ON x.id = r.source_row_id
WHERE r.user_id = @user_id AND r.id = @id;

-- name: ListLabResultRevisions :many
-- Older revisions, newest first.
SELECT * FROM lab_result_revisions WHERE result_id = @result_id ORDER BY revision DESC;
