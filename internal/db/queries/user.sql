-- name: GetUserByEmail :one
SELECT * FROM app_user WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM app_user WHERE id = $1;

-- name: GetUserByRecoverSelector :one
SELECT * FROM app_user WHERE recover_selector = $1;

-- name: GetUserByEmailVerificationSelector :one
SELECT * FROM app_user WHERE email_verification_selector = $1;

-- name: GetUserByOAuth2 :one
SELECT * FROM app_user WHERE oauth2_provider = $1 AND oauth2_uid = $2;

-- name: InsertUser :one
INSERT INTO app_user (id, email, name, password_hash, oauth2_provider, oauth2_uid, avatar_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateUser :one
-- Writes the password only when set_password, and the recovery fields only when set_recovery, so a
-- stale user value cannot restore either. A right-side column reads the old row.
UPDATE app_user
SET name = sqlc.arg(name),
    password_hash = CASE WHEN sqlc.arg(set_password)::boolean THEN sqlc.arg(password_hash) ELSE password_hash END,
    recover_selector = CASE WHEN sqlc.arg(set_recovery)::boolean THEN sqlc.arg(recover_selector) ELSE recover_selector END,
    recover_verifier = CASE WHEN sqlc.arg(set_recovery)::boolean THEN sqlc.arg(recover_verifier) ELSE recover_verifier END,
    recover_token_expiry = CASE WHEN sqlc.arg(set_recovery)::boolean THEN sqlc.arg(recover_token_expiry) ELSE recover_token_expiry END,
    attempt_count = sqlc.arg(attempt_count), last_attempt = sqlc.arg(last_attempt), locked_until = sqlc.arg(locked_until),
    avatar_url = sqlc.arg(avatar_url),
    -- A password change (recovery) ends the open challenge.
    email_verification_selector = CASE WHEN sqlc.arg(set_password)::boolean AND password_hash IS DISTINCT FROM sqlc.arg(password_hash) THEN NULL ELSE email_verification_selector END,
    email_verification_verifier = CASE WHEN sqlc.arg(set_password)::boolean AND password_hash IS DISTINCT FROM sqlc.arg(password_hash) THEN NULL ELSE email_verification_verifier END,
    email_verification_expires_at = CASE WHEN sqlc.arg(set_password)::boolean AND password_hash IS DISTINCT FROM sqlc.arg(password_hash) THEN NULL ELSE email_verification_expires_at END
WHERE id = sqlc.arg(id) AND oauth2_provider IS NOT DISTINCT FROM sqlc.arg(oauth2_provider) AND oauth2_uid IS NOT DISTINCT FROM sqlc.arg(oauth2_uid)
RETURNING *;

-- Login bookkeeping: writes no credential field, so a stale user value cannot restore an old password.
-- name: UpdateUserState :one
UPDATE app_user
SET name = $2, attempt_count = $3, last_attempt = $4, locked_until = $5, avatar_url = $6
WHERE id = $1 AND oauth2_provider IS NOT DISTINCT FROM $7 AND oauth2_uid IS NOT DISTINCT FROM $8
RETURNING *;

-- name: InsertSession :exec
INSERT INTO user_session (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- Starts a session only while the password hash is still the one the login checked.
-- name: InsertSessionForPassword :execrows
INSERT INTO user_session (token_hash, user_id, expires_at)
SELECT $1, $2, $3 FROM app_user WHERE id = $2 AND password_hash = $4;

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

-- name: LockUserForCurrency :one
SELECT * FROM app_user WHERE id = $1 FOR NO KEY UPDATE;

-- name: UpdateUserCurrency :one
UPDATE app_user SET currency_code = $2 WHERE id = $1 RETURNING *;

-- name: SetUserCurrencyLocked :exec
UPDATE app_user SET currency_locked = true WHERE id = $1;

-- Issues a verification challenge: one statement, so the row lock serializes concurrent issues. No row
-- means no issue (verified, locked, inside the 60-second cooldown, or 5 issues in the 24-hour window).
-- name: IssueEmailVerification :one
UPDATE app_user
SET email_verification_selector = sqlc.arg(selector), email_verification_verifier = sqlc.arg(verifier),
    email_verification_expires_at = now() + interval '24 hours',
    email_verification_last_issued_at = now(),
    email_verification_window_started_at = CASE
      WHEN email_verification_window_started_at IS NULL OR email_verification_window_started_at <= now() - interval '24 hours'
      THEN now() ELSE email_verification_window_started_at END,
    email_verification_issued_count = CASE
      WHEN email_verification_window_started_at IS NULL OR email_verification_window_started_at <= now() - interval '24 hours'
      THEN 1 ELSE email_verification_issued_count + 1 END
WHERE email = sqlc.arg(email)
  AND email_verified_at IS NULL
  AND (locked_until IS NULL OR locked_until <= now())
  AND (email_verification_last_issued_at IS NULL OR email_verification_last_issued_at <= now() - interval '60 seconds')
  AND (email_verification_window_started_at IS NULL OR email_verification_window_started_at <= now() - interval '24 hours'
       OR email_verification_issued_count < 5)
RETURNING email;

-- name: MarkEmailVerifiedByGoogle :exec
UPDATE app_user
SET email_verified_at = now(), email_verification_source = 'google', email_verification_exempt = false
WHERE id = $1 AND email_verified_at IS NULL;

-- Records proof of the address and consumes the challenge in one statement. It needs the checked account
-- id, selector, verifier, password hash and email to still match, an unexpired challenge and no active lock.
-- No row means the token no longer works. It uses clock_timestamp(): PostgreSQL re-checks the WHERE after it
-- gets the row lock, and now() would still hold the time from before the wait.
-- name: ConsumeEmailVerification :one
UPDATE app_user
SET email_verified_at = clock_timestamp(), email_verification_source = 'link', email_verification_exempt = false,
    email_verification_selector = NULL, email_verification_verifier = NULL, email_verification_expires_at = NULL
WHERE id = sqlc.arg(id)
  AND email_verification_selector = sqlc.arg(selector)
  AND email_verification_verifier = sqlc.arg(verifier)
  AND email_verification_expires_at > clock_timestamp()
  AND (locked_until IS NULL OR locked_until <= clock_timestamp())
  AND password_hash IS NOT DISTINCT FROM sqlc.narg(password_hash)
  AND email = sqlc.arg(email)
  AND email_verified_at IS NULL
RETURNING *;
