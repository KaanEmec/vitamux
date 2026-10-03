-- Lab documents (J12.1); see docs/architecture/lab-documents.md#storage.

-- name: InsertDocument :exec
INSERT INTO documents (id, user_id, sha256, blob_sha256, filename_ciphertext, size_bytes, page_count, uploaded_by, retention_until)
VALUES (@id, @user_id, @sha256, @blob_sha256, @filename_ciphertext, @size_bytes, @page_count, @uploaded_by, sqlc.narg(retention_until));

-- name: InsertDocumentKey :exec
INSERT INTO document_keys (document_id, ciphertext, key_id) VALUES (@document_id, @ciphertext, @key_id);

-- name: GetDocumentKey :one
SELECT ciphertext FROM document_keys WHERE document_id = @document_id;

-- name: GetLiveDocumentBySHA256 :one
SELECT * FROM documents WHERE user_id = @user_id AND sha256 = @sha256;

-- name: GetDocument :one
SELECT * FROM documents WHERE user_id = @user_id AND id = @id;

-- name: LockDocument :one
SELECT * FROM documents WHERE user_id = @user_id AND id = @id FOR UPDATE;

-- name: ListDocuments :many
-- Newest first; ids are UUIDv7, so id order is upload order. A nil after starts at the newest.
SELECT * FROM documents
WHERE user_id = @user_id AND status <> 'deleted' AND (sqlc.narg(after)::uuid IS NULL OR id < sqlc.narg(after)::uuid)
ORDER BY id DESC
LIMIT @lim;

-- name: DeleteDocumentKey :execrows
DELETE FROM document_keys WHERE document_id = @document_id;

-- name: TombstoneDocument :exec
UPDATE documents SET status = 'deleted', sha256 = NULL, blob_sha256 = NULL, filename_ciphertext = NULL, deleted_at = now()
WHERE id = @id;

-- name: DeleteExtractionRuns :many
-- Runs, their rows and edits go with the original; confirmed results keep copies of what they need.
DELETE FROM extraction_runs WHERE document_id = @document_id RETURNING response_blob_sha256;

-- name: CountDocumentResults :one
SELECT count(*) FROM lab_results r JOIN lab_reports p ON p.id = r.report_id WHERE p.document_id = @document_id;

-- name: DeleteLabReports :execrows
DELETE FROM lab_reports WHERE document_id = @document_id;

-- name: SetDocumentRetention :exec
-- Applies documents.retention_days (null keeps originals) to every live document of the user.
UPDATE documents
SET retention_until = CASE WHEN sqlc.narg(days)::integer IS NULL THEN NULL
                           ELSE uploaded_at + make_interval(days => sqlc.narg(days)::integer) END
WHERE user_id = @user_id AND status <> 'deleted';

-- name: ListDocumentsDue :many
-- Originals the document_retention job deletes: past retention_until, or confirmed while the
-- owner chose to delete originals after confirmation.
SELECT d.user_id, d.id FROM documents d
LEFT JOIN settings s ON s.user_id = d.user_id AND s.key = @after_confirmation_key
WHERE d.status <> 'deleted'
  AND (d.retention_until <= @now::timestamptz OR (d.status = 'confirmed' AND s.value = 'true'::jsonb))
ORDER BY d.id
LIMIT @lim;

-- name: LockDocumentKeysToRotate :many
SELECT document_id, ciphertext FROM document_keys
WHERE key_id <> @key_id
ORDER BY document_id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: ResealDocumentKey :exec
UPDATE document_keys SET ciphertext = @ciphertext, key_id = @key_id WHERE document_id = @document_id;
