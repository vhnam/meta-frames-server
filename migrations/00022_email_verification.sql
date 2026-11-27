-- +goose Up
-- Email proof for an account. Expand only: every existing row keeps null proof, no challenge, and
-- exempt = false. No row is updated here. The access exemption for Google accounts is a one-time
-- operation outside the migrations.
ALTER TABLE app_user
  ADD COLUMN email_verified_at                  timestamptz,
  ADD COLUMN email_verification_source          text,
  ADD COLUMN email_verification_exempt          boolean     NOT NULL DEFAULT false,
  ADD COLUMN email_verification_selector        bytea,
  ADD COLUMN email_verification_verifier        bytea,
  ADD COLUMN email_verification_expires_at      timestamptz,
  ADD COLUMN email_verification_last_issued_at  timestamptz,
  ADD COLUMN email_verification_window_started_at timestamptz,
  ADD COLUMN email_verification_issued_count    integer     NOT NULL DEFAULT 0;

ALTER TABLE app_user
  ADD CONSTRAINT app_user_email_verification_source_check
    CHECK (email_verification_source IN ('link', 'google', 'recovery')),
  ADD CONSTRAINT app_user_email_verification_proof_check
    CHECK ((email_verified_at IS NULL) = (email_verification_source IS NULL)),
  ADD CONSTRAINT app_user_email_verification_exempt_check
    CHECK (NOT (email_verification_exempt AND email_verified_at IS NOT NULL)),
  ADD CONSTRAINT app_user_email_verification_challenge_check
    CHECK ((email_verification_selector IS NULL) = (email_verification_verifier IS NULL)
       AND (email_verification_selector IS NULL) = (email_verification_expires_at IS NULL)),
  ADD CONSTRAINT app_user_email_verification_selector_length_check
    CHECK (octet_length(email_verification_selector) = 64),
  ADD CONSTRAINT app_user_email_verification_verifier_length_check
    CHECK (octet_length(email_verification_verifier) = 64),
  ADD CONSTRAINT app_user_email_verification_issued_count_check
    CHECK (email_verification_issued_count >= 0);

-- The verifier is never indexed: it is compared only after the selector finds the row.
CREATE UNIQUE INDEX app_user_email_verification_selector ON app_user (email_verification_selector)
  WHERE email_verification_selector IS NOT NULL;

-- Shared request counters for the verification endpoints. The key is a SHA-256 hash, never a raw IP.
CREATE TABLE auth_rate_limit (
  bucket            text        NOT NULL,
  key               bytea       NOT NULL,
  window_started_at timestamptz NOT NULL,
  hits              integer     NOT NULL,
  PRIMARY KEY (bucket, key)
);

-- +goose Down
DROP TABLE auth_rate_limit;
DROP INDEX app_user_email_verification_selector;
ALTER TABLE app_user
  DROP CONSTRAINT app_user_email_verification_issued_count_check,
  DROP CONSTRAINT app_user_email_verification_verifier_length_check,
  DROP CONSTRAINT app_user_email_verification_selector_length_check,
  DROP CONSTRAINT app_user_email_verification_challenge_check,
  DROP CONSTRAINT app_user_email_verification_exempt_check,
  DROP CONSTRAINT app_user_email_verification_proof_check,
  DROP CONSTRAINT app_user_email_verification_source_check;
ALTER TABLE app_user
  DROP COLUMN email_verification_issued_count,
  DROP COLUMN email_verification_window_started_at,
  DROP COLUMN email_verification_last_issued_at,
  DROP COLUMN email_verification_expires_at,
  DROP COLUMN email_verification_verifier,
  DROP COLUMN email_verification_selector,
  DROP COLUMN email_verification_exempt,
  DROP COLUMN email_verification_source,
  DROP COLUMN email_verified_at;
