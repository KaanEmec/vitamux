-- Analyte catalogue and aliases (J12.5); see docs/architecture/analyte-catalog.md.

-- name: GetAnalyteID :one
SELECT id FROM analytes WHERE code = @code;

-- name: ListAnalyteAliases :many
-- Seeded aliases first, then the user's own, each in creation order.
SELECT a.id, a.label, a.user_id, a.created_at, an.code AS analyte
FROM analyte_aliases a JOIN analytes an ON an.id = a.analyte_id
WHERE a.user_id IS NULL OR a.user_id = @user_id::uuid
ORDER BY a.user_id NULLS FIRST, a.id;

-- name: InsertAnalyteAlias :one
-- No row when the analyte code is unknown.
INSERT INTO analyte_aliases (analyte_id, label, label_key, user_id, created_by)
SELECT an.id, @label, @label_key, @user_id::uuid, @created_by::text FROM analytes an WHERE an.code = @analyte
RETURNING id, created_at;

-- name: GetAnalyteAlias :one
SELECT a.id, a.label, a.user_id, a.created_at, an.code AS analyte
FROM analyte_aliases a JOIN analytes an ON an.id = a.analyte_id
WHERE a.id = @id AND (a.user_id IS NULL OR a.user_id = @user_id::uuid);

-- name: DeleteOwnerAnalyteAlias :execrows
DELETE FROM analyte_aliases WHERE id = @id AND user_id = @user_id::uuid;

-- name: FindAnalyteByLabel :one
-- The user's alias wins over a seeded one.
SELECT an.code FROM analyte_aliases a JOIN analytes an ON an.id = a.analyte_id
WHERE a.label_key = @label_key AND (a.user_id = @user_id::uuid OR a.user_id IS NULL)
ORDER BY a.user_id NULLS LAST
LIMIT 1;
