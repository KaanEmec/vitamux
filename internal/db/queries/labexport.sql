-- Lab documents in the NDJSON export and importer (J12.6; internal/export/tables.go).
-- Never exported: document_keys, the sealed PDF and filename, raw extractor responses.

-- name: ExportAnalytes :many
SELECT to_jsonb(t)::jsonb AS row FROM analytes t ORDER BY id;

-- name: ExportDocuments :many
SELECT id, (to_jsonb(t) - 'sha256' - 'blob_sha256' - 'filename_ciphertext')::jsonb AS row FROM documents t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportExtractionRuns :many
SELECT id, (to_jsonb(t) - 'response_blob_sha256' - 'job_id')::jsonb AS row FROM extraction_runs t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportLabExtractedRows :many
SELECT t.id::bigint AS id, to_jsonb(t)::jsonb AS row FROM lab_extracted_rows t
JOIN extraction_runs x ON x.id = t.run_id
WHERE x.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportExtractionRowEdits :many
SELECT t.id::bigint AS id, to_jsonb(t)::jsonb AS row FROM extraction_row_edits t
JOIN lab_extracted_rows r ON r.id = t.row_id
JOIN extraction_runs x ON x.id = r.run_id
WHERE x.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportLabReports :many
SELECT id, to_jsonb(t)::jsonb AS row FROM lab_reports t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportLabResults :many
SELECT id, to_jsonb(t)::jsonb AS row FROM lab_results t
WHERE user_id = @user_id AND id > @after::uuid ORDER BY id LIMIT @lim;

-- name: ExportLabResultRevisions :many
SELECT t.id::bigint AS id, to_jsonb(t)::jsonb AS row FROM lab_result_revisions t
JOIN lab_results r ON r.id = t.result_id
WHERE r.user_id = @user_id AND t.id > @after::bigint ORDER BY t.id LIMIT @lim;

-- name: ExportAnalyteAliases :many
-- Owner aliases only; seeded ones come with the migrations.
SELECT id::bigint AS id, to_jsonb(t)::jsonb AS row FROM analyte_aliases t
WHERE user_id = @user_id AND id > @after::bigint ORDER BY id LIMIT @lim;

-- name: ImportDocuments :execrows
-- Rows arrive as tombstones (the importer patches them): the export holds no PDF or key.
INSERT INTO documents
SELECT (p).* FROM jsonb_populate_recordset(NULL::documents, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportLabReports :execrows
INSERT INTO lab_reports
SELECT (p).* FROM jsonb_populate_recordset(NULL::lab_reports, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportLabResults :execrows
INSERT INTO lab_results
SELECT (p).* FROM jsonb_populate_recordset(NULL::lab_results, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- Revisions and aliases get new identity ids: nothing references them, and their natural keys
-- (result and revision; owner and label) make a repeated import skip them.

-- name: ImportLabResultRevisions :execrows
INSERT INTO lab_result_revisions (result_id, revision, snapshot, reason, changed_by, changed_at)
SELECT p.result_id, p.revision, p.snapshot, p.reason, p.changed_by, p.changed_at
FROM jsonb_populate_recordset(NULL::lab_result_revisions, @batch::jsonb) p
ON CONFLICT DO NOTHING;

-- name: ImportAnalyteAliases :execrows
INSERT INTO analyte_aliases (analyte_id, label, label_key, user_id, created_by, created_at)
SELECT p.analyte_id, p.label, p.label_key, p.user_id, p.created_by, p.created_at
FROM jsonb_populate_recordset(NULL::analyte_aliases, @batch::jsonb) p
ON CONFLICT DO NOTHING;
