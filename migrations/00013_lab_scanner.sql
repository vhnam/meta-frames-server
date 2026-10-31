-- +goose Up
-- A lab names the scanners it runs, so a job sent there offers those scanners. Labs differ: one
-- runs a Noritsu HS-1800, another an older Noritsu whose model nobody knows, so a scanner's model
-- becomes optional; the brand stays required.
ALTER TABLE scanner ALTER COLUMN model DROP NOT NULL;
ALTER TABLE scanner DROP CONSTRAINT scanner_model_check;
ALTER TABLE scanner ADD CONSTRAINT scanner_model_check CHECK (model IS NULL OR btrim(model) <> '');

-- A scanner without a model goes by its brand alone: one such scanner per brand.
DROP INDEX scanner_name_live_idx;
CREATE UNIQUE INDEX scanner_name_live_idx ON scanner (owner_id, lower(brand), lower(COALESCE(model, ''))) NULLS NOT DISTINCT
  WHERE deleted_at IS NULL;

-- Rows are replaced as a set by PUT /labs/{id}; removals are real deletes (the audit trigger keeps
-- the history). The lab owns the link; the scanner is a built-in one or the lab owner's own, which
-- the server checks. position keeps the order the scanners were given in: the first is the default.
CREATE TABLE lab_scanner (
  lab_id     uuid        NOT NULL REFERENCES lab(id),
  scanner_id uuid        NOT NULL REFERENCES scanner(id),
  position   integer     NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (lab_id, scanner_id)
);
CREATE INDEX lab_scanner_scanner_idx ON lab_scanner (scanner_id);

CREATE TRIGGER lab_scanner_audit AFTER INSERT OR UPDATE OR DELETE ON lab_scanner FOR EACH ROW EXECUTE FUNCTION audit_row('lab_id');

-- +goose Down
DROP TRIGGER lab_scanner_audit ON lab_scanner;
DELETE FROM audit_log WHERE entity_type = 'lab_scanner';
DROP TABLE lab_scanner;

-- A scanner without a model takes its brand as the model again.
DROP INDEX scanner_name_live_idx;
ALTER TABLE scanner DISABLE TRIGGER USER;
UPDATE scanner SET model = brand WHERE model IS NULL;
ALTER TABLE scanner ENABLE TRIGGER USER;
ALTER TABLE scanner DROP CONSTRAINT scanner_model_check;
ALTER TABLE scanner ADD CONSTRAINT scanner_model_check CHECK (btrim(model) <> '');
ALTER TABLE scanner ALTER COLUMN model SET NOT NULL;
CREATE UNIQUE INDEX scanner_name_live_idx ON scanner (owner_id, lower(brand), lower(model)) NULLS NOT DISTINCT
  WHERE deleted_at IS NULL;
