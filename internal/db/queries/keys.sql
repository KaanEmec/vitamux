-- Key rotation (J03.1). Each Lock query row-locks its batch; skipped (locked) rows are left for a later run.

-- name: LockCredentialsToRotate :many
SELECT connection_id, ciphertext
FROM credentials
WHERE key_id <> @key_id
ORDER BY connection_id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: ResealCredential :exec
UPDATE credentials SET ciphertext = @ciphertext, key_id = @key_id
WHERE connection_id = @connection_id;

-- name: LockTOTPToRotate :many
SELECT id, totp_ciphertext
FROM users
WHERE totp_key_id <> @key_id
ORDER BY id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: ResealTOTP :exec
UPDATE users SET totp_ciphertext = @ciphertext, totp_key_id = @key_id
WHERE id = @id;
