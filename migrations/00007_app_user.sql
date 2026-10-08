-- +goose Up
-- Accounts for authboss. "user" is a reserved word, hence app_user. Emails are stored
-- lower-cased by the application, so the unique index is case-insensitive in practice.
-- No audit trigger: the audit log copies whole rows and would keep password hashes.
CREATE TABLE app_user (
  id                   uuid PRIMARY KEY,
  email                text NOT NULL UNIQUE,
  name                 text,
  password_hash        text NOT NULL,
  recover_selector     text UNIQUE,
  recover_verifier     text,
  recover_token_expiry timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER app_user_set_updated_at BEFORE UPDATE ON app_user FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE app_user;
