-- name: InsertAuditEvent :one
INSERT INTO audit_events (user_id, actor, action, target_type, target_id, detail)
VALUES (@user_id, @actor, @action, @target_type, @target_id, @detail)
RETURNING id, occurred_at;
