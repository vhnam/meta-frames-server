-- +goose Up
-- Scanners move from a fixed list (noritsu, frontier, other) to their own table.
--
-- owner_id NULL marks the built-in catalog, which every account sees and nobody edits; any other
-- scanner belongs to one account. The catalog starts with the two lab minilabs; an account adds
-- its own scanners (another lab's machine, a flatbed, a camera rig) instead of picking "other".
--
-- The ids of the catalog rows are fixed: the code maps the old scanner codes to them.
CREATE TABLE scanner (
  id         uuid PRIMARY KEY,
  owner_id   uuid REFERENCES app_user(id),
  brand      text NOT NULL CHECK (btrim(brand) <> ''),
  model      text NOT NULL CHECK (btrim(model) <> ''),
  -- NULL when unknown, as for the scanners made below from "other".
  kind       text CHECK (kind IN ('minilab', 'virtual_drum', 'drum', 'dedicated_film', 'flatbed', 'camera')),
  notes      text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
-- One live scanner per name within the catalog and within each account.
CREATE UNIQUE INDEX scanner_name_live_idx ON scanner (owner_id, lower(brand), lower(model)) NULLS NOT DISTINCT
  WHERE deleted_at IS NULL;
CREATE INDEX scanner_owner_idx ON scanner (owner_id);

INSERT INTO scanner (id, owner_id, brand, model, kind) VALUES
  ('d177cb3c-a850-4232-a4ca-75189b7b279b', NULL, 'Noritsu', 'HS-1800', 'minilab'),
  ('e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc', NULL, 'Frontier', 'SP-3000', 'minilab');

-- "other" goes away: each account that used it gets its own "Unknown scanner" to rename later.
-- Rows still without an owner (no account exists yet) share one with a fixed id, which
-- claim_unowned_data() hands to the first account with the rest of the data.
INSERT INTO scanner (id, owner_id, brand, model)
SELECT gen_random_uuid(), owners.owner_id, 'Unknown', 'scanner'
FROM (
  SELECT owner_id FROM scan WHERE scanner = 'other'
  UNION
  SELECT p.owner_id FROM processing_scan_order o JOIN processing p ON p.id = o.processing_id WHERE o.scanner = 'other'
) AS owners
WHERE owners.owner_id IS NOT NULL;

INSERT INTO scanner (id, owner_id, brand, model)
SELECT '93d019be-a6d0-4e7a-aaa3-72acbc2f8f1c', NULL, 'Unknown', 'scanner'
WHERE EXISTS (SELECT 1 FROM scan WHERE scanner = 'other' AND owner_id IS NULL)
   OR EXISTS (SELECT 1 FROM processing_scan_order o JOIN processing p ON p.id = o.processing_id
              WHERE o.scanner = 'other' AND p.owner_id IS NULL);

-- +goose StatementBegin
-- The scanner an old code stands for, seen from the account that owns the row.
CREATE FUNCTION scanner_for_code(code text, account uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT CASE code
    WHEN 'noritsu'  THEN 'd177cb3c-a850-4232-a4ca-75189b7b279b'::uuid
    WHEN 'frontier' THEN 'e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc'::uuid
    ELSE COALESCE(
      (SELECT id FROM scanner WHERE owner_id = account AND brand = 'Unknown' AND model = 'scanner'),
      '93d019be-a6d0-4e7a-aaa3-72acbc2f8f1c'::uuid)
  END;
$$;
-- +goose StatementEnd

ALTER TABLE scan                  ADD COLUMN scanner_id uuid REFERENCES scanner(id);
ALTER TABLE processing_scan_order ADD COLUMN scanner_id uuid REFERENCES scanner(id);

-- Moving to the new column is not an edit: no audit entry, no new updated_at.
ALTER TABLE scan                  DISABLE TRIGGER USER;
ALTER TABLE processing_scan_order DISABLE TRIGGER USER;
UPDATE scan SET scanner_id = scanner_for_code(scanner, owner_id);
UPDATE processing_scan_order o SET scanner_id = scanner_for_code(o.scanner, p.owner_id)
FROM processing p WHERE p.id = o.processing_id;
ALTER TABLE scan                  ENABLE TRIGGER USER;
ALTER TABLE processing_scan_order ENABLE TRIGGER USER;

DROP FUNCTION scanner_for_code(text, uuid);

ALTER TABLE scan ALTER COLUMN scanner_id SET NOT NULL;
DROP INDEX scan_slot_live_idx;
ALTER TABLE scan DROP COLUMN scanner;
CREATE UNIQUE INDEX scan_slot_live_idx ON scan (processing_id, frame_id, scanner_id) WHERE deleted_at IS NULL;
CREATE INDEX scan_scanner_idx ON scan (scanner_id);

ALTER TABLE processing_scan_order ALTER COLUMN scanner_id SET NOT NULL;
ALTER TABLE processing_scan_order DROP CONSTRAINT processing_scan_order_pkey;
ALTER TABLE processing_scan_order DROP COLUMN scanner;
ALTER TABLE processing_scan_order ADD PRIMARY KEY (processing_id, scanner_id);
CREATE INDEX processing_scan_order_scanner_idx ON processing_scan_order (scanner_id);

CREATE TRIGGER scanner_set_updated_at BEFORE UPDATE ON scanner FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER scanner_audit AFTER INSERT OR UPDATE OR DELETE ON scanner FOR EACH ROW EXECUTE FUNCTION audit_row('id');

-- +goose StatementBegin
-- Same as before, plus the shared "Unknown scanner" made above for rows without an owner.
-- Catalog scanners also have no owner, so only that one row is claimed.
CREATE OR REPLACE FUNCTION claim_unowned_data(new_owner uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('claim_unowned_data'));
  -- Parents before children, so each composite foreign key finds its parent already moved.
  UPDATE camera     SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lens       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE film_stock SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lab        SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scanner    SET owner_id = new_owner WHERE owner_id IS NULL AND id = '93d019be-a6d0-4e7a-aaa3-72acbc2f8f1c';
  UPDATE roll       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE processing SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE frame      SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scan       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE audit_log  SET owner_id = new_owner WHERE owner_id IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION claim_unowned_data(new_owner uuid) RETURNS void LANGUAGE plpgsql AS $$
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

DROP TRIGGER scanner_audit ON scanner;
DROP TRIGGER scanner_set_updated_at ON scanner;

-- Every scanner outside the catalog folds back into "other", so two of them on one job collapse
-- into one order row, and two live scans of one frame keep only the oldest.
ALTER TABLE scan                  ADD COLUMN scanner text;
ALTER TABLE processing_scan_order ADD COLUMN scanner text;

ALTER TABLE scan                  DISABLE TRIGGER USER;
ALTER TABLE processing_scan_order DISABLE TRIGGER USER;
UPDATE scan SET scanner = CASE scanner_id
  WHEN 'd177cb3c-a850-4232-a4ca-75189b7b279b' THEN 'noritsu'
  WHEN 'e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc' THEN 'frontier'
  ELSE 'other' END;
UPDATE processing_scan_order SET scanner = CASE scanner_id
  WHEN 'd177cb3c-a850-4232-a4ca-75189b7b279b' THEN 'noritsu'
  WHEN 'e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc' THEN 'frontier'
  ELSE 'other' END;
UPDATE scan SET deleted_at = now()
WHERE id IN (
  SELECT id FROM (
    SELECT id, row_number() OVER (PARTITION BY processing_id, frame_id, scanner ORDER BY created_at, id) AS position
    FROM scan WHERE deleted_at IS NULL
  ) AS ranked WHERE position > 1
);
DELETE FROM processing_scan_order
WHERE (processing_id, scanner_id) IN (
  SELECT processing_id, scanner_id FROM (
    SELECT processing_id, scanner_id,
           row_number() OVER (PARTITION BY processing_id, scanner ORDER BY hi_res DESC, created_at, scanner_id) AS position
    FROM processing_scan_order
  ) AS ranked WHERE position > 1
);
ALTER TABLE scan                  ENABLE TRIGGER USER;
ALTER TABLE processing_scan_order ENABLE TRIGGER USER;

DROP INDEX processing_scan_order_scanner_idx;
ALTER TABLE processing_scan_order DROP CONSTRAINT processing_scan_order_pkey;
ALTER TABLE processing_scan_order DROP COLUMN scanner_id;
ALTER TABLE processing_scan_order ALTER COLUMN scanner SET NOT NULL;
ALTER TABLE processing_scan_order ADD CONSTRAINT processing_scan_order_scanner_check CHECK (scanner IN ('noritsu', 'frontier', 'other'));
ALTER TABLE processing_scan_order ADD PRIMARY KEY (processing_id, scanner);

DROP INDEX scan_scanner_idx;
DROP INDEX scan_slot_live_idx;
ALTER TABLE scan DROP COLUMN scanner_id;
ALTER TABLE scan ALTER COLUMN scanner SET NOT NULL;
ALTER TABLE scan ADD CONSTRAINT scan_scanner_check CHECK (scanner IN ('noritsu', 'frontier', 'other'));
CREATE UNIQUE INDEX scan_slot_live_idx ON scan (processing_id, frame_id, scanner) WHERE deleted_at IS NULL;

DELETE FROM audit_log WHERE entity_type = 'scanner';
DROP TABLE scanner;
