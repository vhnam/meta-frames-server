-- name: GetUserByEmail :one
SELECT * FROM app_user WHERE email = $1;

-- name: GetUserByRecoverSelector :one
SELECT * FROM app_user WHERE recover_selector = $1;

-- name: GetUserByOAuth2 :one
SELECT * FROM app_user WHERE oauth2_provider = $1 AND oauth2_uid = $2;

-- name: InsertUser :one
INSERT INTO app_user (id, email, name, password_hash, oauth2_provider, oauth2_uid)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateUser :one
UPDATE app_user
SET name = $2, password_hash = $3, recover_selector = $4, recover_verifier = $5, recover_token_expiry = $6,
    attempt_count = $7, last_attempt = $8, locked_until = $9, oauth2_provider = $10, oauth2_uid = $11
WHERE id = $1
RETURNING *;

-- name: InsertSession :exec
INSERT INTO user_session (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- name: GetSessionUser :one
SELECT app_user.* FROM user_session JOIN app_user ON app_user.id = user_session.user_id
WHERE user_session.token_hash = $1 AND user_session.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM user_session WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM user_session WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM user_session WHERE expires_at <= now();

-- Hands records created before accounts existed to a new account (see 00010_ownership.sql).
-- name: ClaimUnownedData :exec
SELECT claim_unowned_data($1);
