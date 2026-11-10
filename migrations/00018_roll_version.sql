-- +goose Up
-- A roll's version grows by one on every change to the row, whichever query makes it, so a
-- client can send back the version it loaded (If-Match) and refuse to overwrite a newer change.
-- updated_at cannot do this: now() is the transaction start, so it is neither monotonic nor
-- distinct between two changes. An update that changes nothing but updated_at keeps the version,
-- which also keeps such no-op updates out of the audit log.
ALTER TABLE roll ADD COLUMN version bigint NOT NULL DEFAULT 0;

-- +goose StatementBegin
CREATE FUNCTION bump_roll_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - 'updated_at' - 'version') IS DISTINCT FROM (to_jsonb(OLD) - 'updated_at' - 'version') THEN
    NEW.version = OLD.version + 1;
  ELSE
    NEW.version = OLD.version;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER roll_bump_version BEFORE UPDATE ON roll FOR EACH ROW EXECUTE FUNCTION bump_roll_version();

-- +goose Down
DROP TRIGGER roll_bump_version ON roll;
DROP FUNCTION bump_roll_version();
ALTER TABLE roll DISABLE TRIGGER USER;
ALTER TABLE roll DROP COLUMN version;
ALTER TABLE roll ENABLE TRIGGER USER;
