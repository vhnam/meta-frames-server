-- +goose Up
-- Every entity table gets created_at / updated_at / deleted_at. Rows are never removed by the
-- API: "deleting" sets deleted_at, and reads filter on deleted_at IS NULL.
ALTER TABLE camera     ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE lens       ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE film_stock ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE lab        ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE roll       ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE processing ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE scan       ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN deleted_at timestamptz;
ALTER TABLE frame      ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
                       ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
                       ADD COLUMN deleted_at timestamptz;

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER camera_set_updated_at     BEFORE UPDATE ON camera     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER lens_set_updated_at       BEFORE UPDATE ON lens       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER film_stock_set_updated_at BEFORE UPDATE ON film_stock FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER lab_set_updated_at        BEFORE UPDATE ON lab        FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER roll_set_updated_at       BEFORE UPDATE ON roll       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER processing_set_updated_at BEFORE UPDATE ON processing FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER frame_set_updated_at      BEFORE UPDATE ON frame      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER scan_set_updated_at       BEFORE UPDATE ON scan       FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Audit trail: one row per change, written by triggers so no code path can skip it.
-- The API sets app.request_id / app.actor for the transaction (see db.PostgresStore).
CREATE TABLE audit_log (
  id          bigserial PRIMARY KEY,
  entity_type text        NOT NULL,
  entity_id   uuid        NOT NULL,
  action      text        NOT NULL CHECK (action IN ('create', 'update', 'delete')),
  before      jsonb,
  after       jsonb,
  actor       text,
  request_id  text,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id, id DESC);

-- +goose StatementBegin
-- TG_ARGV[0] names the column that identifies the audited entity (usually "id").
CREATE FUNCTION audit_row() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE TRIGGER camera_audit      AFTER INSERT OR UPDATE OR DELETE ON camera      FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER lens_audit        AFTER INSERT OR UPDATE OR DELETE ON lens        FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER film_stock_audit  AFTER INSERT OR UPDATE OR DELETE ON film_stock  FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER lab_audit         AFTER INSERT OR UPDATE OR DELETE ON lab         FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER roll_audit        AFTER INSERT OR UPDATE OR DELETE ON roll        FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER processing_audit  AFTER INSERT OR UPDATE OR DELETE ON processing  FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER frame_audit       AFTER INSERT OR UPDATE OR DELETE ON frame       FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER scan_audit        AFTER INSERT OR UPDATE OR DELETE ON scan        FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER camera_lens_audit AFTER INSERT OR UPDATE OR DELETE ON camera_lens FOR EACH ROW EXECUTE FUNCTION audit_row('camera_id');
CREATE TRIGGER roll_lens_audit   AFTER INSERT OR UPDATE OR DELETE ON roll_lens   FOR EACH ROW EXECUTE FUNCTION audit_row('roll_id');

-- +goose Down
DROP TRIGGER roll_lens_audit ON roll_lens;
DROP TRIGGER camera_lens_audit ON camera_lens;
DROP TRIGGER scan_audit ON scan;
DROP TRIGGER frame_audit ON frame;
DROP TRIGGER processing_audit ON processing;
DROP TRIGGER roll_audit ON roll;
DROP TRIGGER lab_audit ON lab;
DROP TRIGGER film_stock_audit ON film_stock;
DROP TRIGGER lens_audit ON lens;
DROP TRIGGER camera_audit ON camera;
DROP FUNCTION audit_row();
DROP TABLE audit_log;
DROP TRIGGER scan_set_updated_at ON scan;
DROP TRIGGER frame_set_updated_at ON frame;
DROP TRIGGER processing_set_updated_at ON processing;
DROP TRIGGER roll_set_updated_at ON roll;
DROP TRIGGER lab_set_updated_at ON lab;
DROP TRIGGER film_stock_set_updated_at ON film_stock;
DROP TRIGGER lens_set_updated_at ON lens;
DROP TRIGGER camera_set_updated_at ON camera;
DROP FUNCTION set_updated_at();
ALTER TABLE frame      DROP COLUMN deleted_at, DROP COLUMN updated_at, DROP COLUMN created_at;
ALTER TABLE scan       DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE processing DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE roll       DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE lab        DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE film_stock DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE lens       DROP COLUMN deleted_at, DROP COLUMN updated_at;
ALTER TABLE camera     DROP COLUMN deleted_at, DROP COLUMN updated_at;
