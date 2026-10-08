-- +goose Up
-- Every record belongs to one account, and an account only sees its own records.
--
-- owner_id stays nullable for rows created before accounts existed. They go to the earliest
-- account now; if there is none yet, claim_unowned_data() hands them to the first account that
-- is created. Rows without an owner match no query.
--
-- Composite foreign keys ((parent_id, owner_id) -> parent (id, owner_id)) make the database refuse
-- a record that points at another account's record. The link tables (camera_lens, roll_lens,
-- processing_scan_order) have no owner of their own; they are reached through their owned parents.

ALTER TABLE camera     ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE lens       ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE film_stock ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE lab        ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE roll       ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE processing ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE frame      ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE scan       ADD COLUMN owner_id uuid REFERENCES app_user(id);
ALTER TABLE audit_log  ADD COLUMN owner_id uuid REFERENCES app_user(id) ON DELETE CASCADE;

ALTER TABLE camera     ADD CONSTRAINT camera_id_owner     UNIQUE (id, owner_id);
ALTER TABLE lens       ADD CONSTRAINT lens_id_owner       UNIQUE (id, owner_id);
ALTER TABLE film_stock ADD CONSTRAINT film_stock_id_owner UNIQUE (id, owner_id);
ALTER TABLE lab        ADD CONSTRAINT lab_id_owner        UNIQUE (id, owner_id);
ALTER TABLE roll       ADD CONSTRAINT roll_id_owner       UNIQUE (id, owner_id);
ALTER TABLE processing ADD CONSTRAINT processing_id_owner UNIQUE (id, owner_id);
ALTER TABLE frame      ADD CONSTRAINT frame_id_owner      UNIQUE (id, owner_id);

-- +goose StatementBegin
-- Gives every record without an owner to the account. The advisory lock keeps two accounts
-- created at the same moment from splitting the records between them.
CREATE FUNCTION claim_unowned_data(new_owner uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('claim_unowned_data'));
  -- Parents before children, so each composite foreign key finds its parent already moved.
  UPDATE camera     SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lens       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE film_stock SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lab        SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE roll       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE processing SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE frame      SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scan       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE audit_log  SET owner_id = new_owner WHERE owner_id IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Audit entries belong to the owner of the audited row; link-table rows have no owner column, so
-- they take the account of the request (app.owner_id, set per transaction like app.actor).
CREATE OR REPLACE FUNCTION audit_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  id_column text := TG_ARGV[0];
  before_row jsonb;
  after_row  jsonb;
  action_name text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    action_name := 'create';
    after_row := to_jsonb(NEW);
  ELSIF TG_OP = 'UPDATE' THEN
    before_row := to_jsonb(OLD);
    after_row := to_jsonb(NEW);
    -- A no-op update (only updated_at moved) is not worth a log entry.
    IF (before_row - 'updated_at') = (after_row - 'updated_at') THEN
      RETURN NULL;
    END IF;
    -- Handing a record without an owner to an account (claim_unowned_data) is not a change.
    IF before_row->>'owner_id' IS NULL AND after_row->>'owner_id' IS NOT NULL
       AND (before_row - 'updated_at' - 'owner_id') = (after_row - 'updated_at' - 'owner_id') THEN
      RETURN NULL;
    END IF;
    IF before_row->>'deleted_at' IS NULL AND after_row->>'deleted_at' IS NOT NULL THEN
      action_name := 'delete';
    ELSE
      action_name := 'update';
    END IF;
  ELSE
    action_name := 'delete';
    before_row := to_jsonb(OLD);
  END IF;

  INSERT INTO audit_log (entity_type, entity_id, action, before, after, actor, request_id, owner_id)
  VALUES (TG_TABLE_NAME, (COALESCE(after_row, before_row)->>id_column)::uuid, action_name, before_row, after_row,
          NULLIF(current_setting('app.actor', true), ''), NULLIF(current_setting('app.request_id', true), ''),
          COALESCE((COALESCE(after_row, before_row)->>'owner_id')::uuid,
                   NULLIF(current_setting('app.owner_id', true), '')::uuid));
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

SELECT claim_unowned_data(id) FROM (SELECT id FROM app_user ORDER BY created_at, id LIMIT 1) AS first_account;

ALTER TABLE film_stock ADD CONSTRAINT film_stock_base_same_owner FOREIGN KEY (base_stock_id, owner_id) REFERENCES film_stock (id, owner_id);
ALTER TABLE roll       ADD CONSTRAINT roll_film_stock_same_owner FOREIGN KEY (film_stock_id, owner_id) REFERENCES film_stock (id, owner_id);
ALTER TABLE roll       ADD CONSTRAINT roll_camera_same_owner     FOREIGN KEY (camera_id, owner_id)     REFERENCES camera (id, owner_id);
ALTER TABLE processing ADD CONSTRAINT processing_roll_same_owner FOREIGN KEY (roll_id, owner_id)       REFERENCES roll (id, owner_id);
ALTER TABLE processing ADD CONSTRAINT processing_lab_same_owner  FOREIGN KEY (lab_id, owner_id)        REFERENCES lab (id, owner_id);
ALTER TABLE frame      ADD CONSTRAINT frame_roll_same_owner      FOREIGN KEY (roll_id, owner_id)       REFERENCES roll (id, owner_id);
ALTER TABLE scan       ADD CONSTRAINT scan_processing_same_owner FOREIGN KEY (processing_id, owner_id) REFERENCES processing (id, owner_id);
ALTER TABLE scan       ADD CONSTRAINT scan_frame_same_owner      FOREIGN KEY (frame_id, owner_id)      REFERENCES frame (id, owner_id);

CREATE INDEX camera_owner_idx     ON camera (owner_id);
CREATE INDEX lens_owner_idx       ON lens (owner_id);
CREATE INDEX film_stock_owner_idx ON film_stock (owner_id);
CREATE INDEX lab_owner_idx        ON lab (owner_id);
CREATE INDEX roll_owner_idx       ON roll (owner_id, created_at DESC);
CREATE INDEX processing_owner_idx ON processing (owner_id);
CREATE INDEX audit_log_owner_idx  ON audit_log (owner_id, id DESC);

-- Idempotency keys are per account. Old keys only cached responses, so they are dropped.
DELETE FROM idempotency_key;
ALTER TABLE idempotency_key DROP CONSTRAINT idempotency_key_pkey;
ALTER TABLE idempotency_key ADD COLUMN owner_id uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE;
ALTER TABLE idempotency_key ADD PRIMARY KEY (owner_id, key);


-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  id_column text := TG_ARGV[0];
  before_row jsonb;
  after_row  jsonb;
  action_name text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    action_name := 'create';
    after_row := to_jsonb(NEW);
  ELSIF TG_OP = 'UPDATE' THEN
    before_row := to_jsonb(OLD);
    after_row := to_jsonb(NEW);
    IF (before_row - 'updated_at') = (after_row - 'updated_at') THEN
      RETURN NULL;
    END IF;
    IF before_row->>'deleted_at' IS NULL AND after_row->>'deleted_at' IS NOT NULL THEN
      action_name := 'delete';
    ELSE
      action_name := 'update';
    END IF;
  ELSE
    action_name := 'delete';
    before_row := to_jsonb(OLD);
  END IF;

  INSERT INTO audit_log (entity_type, entity_id, action, before, after, actor, request_id)
  VALUES (TG_TABLE_NAME, (COALESCE(after_row, before_row)->>id_column)::uuid, action_name, before_row, after_row,
          NULLIF(current_setting('app.actor', true), ''), NULLIF(current_setting('app.request_id', true), ''));
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

ALTER TABLE idempotency_key DROP CONSTRAINT idempotency_key_pkey;
ALTER TABLE idempotency_key DROP COLUMN owner_id;
DELETE FROM idempotency_key;
ALTER TABLE idempotency_key ADD PRIMARY KEY (key);

DROP INDEX audit_log_owner_idx;
DROP INDEX processing_owner_idx;
DROP INDEX roll_owner_idx;
DROP INDEX lab_owner_idx;
DROP INDEX film_stock_owner_idx;
DROP INDEX lens_owner_idx;
DROP INDEX camera_owner_idx;

ALTER TABLE scan       DROP CONSTRAINT scan_frame_same_owner;
ALTER TABLE scan       DROP CONSTRAINT scan_processing_same_owner;
ALTER TABLE frame      DROP CONSTRAINT frame_roll_same_owner;
ALTER TABLE processing DROP CONSTRAINT processing_lab_same_owner;
ALTER TABLE processing DROP CONSTRAINT processing_roll_same_owner;
ALTER TABLE roll       DROP CONSTRAINT roll_camera_same_owner;
ALTER TABLE roll       DROP CONSTRAINT roll_film_stock_same_owner;
ALTER TABLE film_stock DROP CONSTRAINT film_stock_base_same_owner;

DROP FUNCTION claim_unowned_data(uuid);

ALTER TABLE frame      DROP CONSTRAINT frame_id_owner;
ALTER TABLE processing DROP CONSTRAINT processing_id_owner;
ALTER TABLE roll       DROP CONSTRAINT roll_id_owner;
ALTER TABLE lab        DROP CONSTRAINT lab_id_owner;
ALTER TABLE film_stock DROP CONSTRAINT film_stock_id_owner;
ALTER TABLE lens       DROP CONSTRAINT lens_id_owner;
ALTER TABLE camera     DROP CONSTRAINT camera_id_owner;

ALTER TABLE audit_log  DROP COLUMN owner_id;
ALTER TABLE scan       DROP COLUMN owner_id;
ALTER TABLE frame      DROP COLUMN owner_id;
ALTER TABLE processing DROP COLUMN owner_id;
ALTER TABLE roll       DROP COLUMN owner_id;
ALTER TABLE lab        DROP COLUMN owner_id;
ALTER TABLE film_stock DROP COLUMN owner_id;
ALTER TABLE lens       DROP COLUMN owner_id;
ALTER TABLE camera     DROP COLUMN owner_id;
