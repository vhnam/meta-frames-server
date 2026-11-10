-- +goose Up
-- What the owner plans for a roll: where it ends (developed only, or scanned too) and who does
-- each step. All optional: a roll in stock or in a camera usually has no plan yet.
ALTER TABLE roll ADD COLUMN target     text CHECK (target IN ('developed', 'scanned'));
ALTER TABLE roll ADD COLUMN develop_by text CHECK (develop_by IN ('self', 'lab'));
ALTER TABLE roll ADD COLUMN scan_by    text CHECK (scan_by IN ('self', 'lab'));
-- Who scans only matters when the roll is to be scanned.
ALTER TABLE roll ADD CONSTRAINT roll_scan_by_needs_scan CHECK (scan_by IS NULL OR target IS DISTINCT FROM 'developed');
-- "Needs a scan" (first scan or rescan): set by the owner, cleared when a scan job starts.
ALTER TABLE roll ADD COLUMN scan_requested_at timestamptz;

-- The day the film was developed, apart from the day the negatives came home.
ALTER TABLE processing ADD COLUMN developed_at date;

-- One physical stay of one roll at one lab: from hand-over to return. Jobs at the lab during the
-- stay point at it; cancelling a job does not end the stay. Rolls handed over together share a
-- batch_id.
CREATE TABLE lab_handoff (
  id          uuid PRIMARY KEY,
  owner_id    uuid NOT NULL REFERENCES app_user(id),
  roll_id     uuid NOT NULL,
  lab_id      uuid NOT NULL,
  batch_id    uuid NOT NULL,
  handed_at   date NOT NULL,
  returned_at date,
  notes       text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK (returned_at IS NULL OR returned_at >= handed_at),
  CONSTRAINT lab_handoff_id_owner UNIQUE (id, owner_id),
  CONSTRAINT lab_handoff_roll_same_owner FOREIGN KEY (roll_id, owner_id) REFERENCES roll (id, owner_id),
  CONSTRAINT lab_handoff_lab_same_owner FOREIGN KEY (lab_id, owner_id) REFERENCES lab (id, owner_id)
);
-- A roll is at one lab at a time.
CREATE UNIQUE INDEX lab_handoff_one_open_per_roll ON lab_handoff (roll_id) WHERE returned_at IS NULL;
CREATE INDEX lab_handoff_batch_idx ON lab_handoff (batch_id);

ALTER TABLE processing ADD COLUMN handoff_id uuid;
ALTER TABLE processing ADD CONSTRAINT processing_handoff_same_owner
  FOREIGN KEY (handoff_id, owner_id) REFERENCES lab_handoff (id, owner_id);
CREATE INDEX processing_handoff_idx ON processing (handoff_id);

CREATE TRIGGER lab_handoff_set_updated_at BEFORE UPDATE ON lab_handoff FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER lab_handoff_audit AFTER INSERT OR UPDATE OR DELETE ON lab_handoff FOR EACH ROW EXECUTE FUNCTION audit_row('id');

-- +goose Down
DROP INDEX processing_handoff_idx;
ALTER TABLE processing DROP CONSTRAINT processing_handoff_same_owner;
ALTER TABLE processing DROP COLUMN handoff_id;
DELETE FROM audit_log WHERE entity_type = 'lab_handoff';
DROP TABLE lab_handoff;
ALTER TABLE processing DROP COLUMN developed_at;
ALTER TABLE roll DROP COLUMN scan_requested_at;
ALTER TABLE roll DROP CONSTRAINT roll_scan_by_needs_scan;
ALTER TABLE roll DROP COLUMN scan_by;
ALTER TABLE roll DROP COLUMN develop_by;
ALTER TABLE roll DROP COLUMN target;
