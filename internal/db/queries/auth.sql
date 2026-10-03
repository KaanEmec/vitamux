-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (id, username, password_hash) VALUES (@id, @username, @password_hash);

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: SetPendingTOTP :execrows
UPDATE users
SET totp_ciphertext = @totp_ciphertext, totp_key_id = @totp_key_id::text, totp_last_step = NULL, updated_at = now()
WHERE id = @id AND totp_enabled_at IS NULL;

-- name: EnableTOTP :execrows
UPDATE users SET totp_enabled_at = @now::timestamptz, totp_last_step = @step::bigint, updated_at = now()
WHERE id = @id AND totp_ciphertext IS NOT NULL AND totp_enabled_at IS NULL;

-- name: UseTOTPStep :execrows
UPDATE users SET totp_last_step = @step::bigint
WHERE id = @id AND totp_enabled_at IS NOT NULL AND (totp_last_step IS NULL OR totp_last_step < @step);

-- name: DisableTOTP :exec
UPDATE users
SET totp_ciphertext = NULL, totp_key_id = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = now()
WHERE id = @id;

-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash) VALUES (@user_id, @code_hash);

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = @user_id;

-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = @now::timestamptz
WHERE user_id = @user_id AND code_hash = @code_hash AND used_at IS NULL;

-- name: InsertSession :exec
INSERT INTO sessions (id, user_id, token_hash, created_at, last_seen_at, expires_at)
VALUES (@id, @user_id, @token_hash, @now, @now, @expires_at);

-- name: GetLiveSession :one
SELECT * FROM sessions WHERE token_hash = @token_hash AND expires_at > @now AND last_seen_at > @idle_since;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = @now WHERE id = @id AND last_seen_at < @before;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = @id;

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions WHERE token_hash = @token_hash;

-- name: DeleteStaleSessions :exec
DELETE FROM sessions WHERE user_id = @user_id AND (expires_at <= @now OR last_seen_at <= @idle_since);

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id;

-- name: ListLiveSessions :many
SELECT id, created_at, last_seen_at, expires_at FROM sessions
WHERE user_id = @user_id AND expires_at > @now AND last_seen_at > @idle_since
ORDER BY last_seen_at DESC, id;

-- name: DeleteUserSession :execrows
DELETE FROM sessions WHERE id = @id AND user_id = @user_id;

-- name: DeleteOtherSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id AND id <> @keep;
