-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = @username;
