-- +goose Up
-- One roll can be billed on several lines of an order: one line for developing, one for scanning.
-- A job therefore links to lines through its own table. Rows are replaced as a set; removals are
-- real deletes (the audit trigger keeps the history). The server keeps a job's lines on one order.
CREATE TABLE processing_order_item (
  processing_id uuid        NOT NULL REFERENCES processing(id),
  order_item_id uuid        NOT NULL REFERENCES lab_order_item(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (processing_id, order_item_id)
);
CREATE INDEX processing_order_item_item_idx ON processing_order_item (order_item_id);

INSERT INTO processing_order_item (processing_id, order_item_id)
SELECT id, order_item_id FROM processing WHERE order_item_id IS NOT NULL;

DROP INDEX processing_order_item_idx;
ALTER TABLE processing DISABLE TRIGGER USER;
ALTER TABLE processing DROP COLUMN order_item_id;
ALTER TABLE processing ENABLE TRIGGER USER;

-- What a job on an order costs: its lines added up.
CREATE VIEW processing_order_cost AS
SELECT link.processing_id, sum(cost.unit_cost)::int AS cost
FROM processing_order_item link
JOIN lab_order_item_cost cost ON cost.item_id = link.order_item_id
GROUP BY link.processing_id;

CREATE TRIGGER processing_order_item_audit AFTER INSERT OR UPDATE OR DELETE ON processing_order_item FOR EACH ROW EXECUTE FUNCTION audit_row('processing_id');

-- +goose Down
DROP TRIGGER processing_order_item_audit ON processing_order_item;
DROP VIEW processing_order_cost;

ALTER TABLE processing ADD COLUMN order_item_id uuid;
ALTER TABLE processing ADD CONSTRAINT processing_order_item_same_owner
  FOREIGN KEY (order_item_id, owner_id) REFERENCES lab_order_item (id, owner_id);
CREATE INDEX processing_order_item_idx ON processing (order_item_id);

-- A job goes back to one line: its first.
ALTER TABLE processing DISABLE TRIGGER USER;
UPDATE processing p SET order_item_id = (
  SELECT link.order_item_id FROM processing_order_item link
  JOIN lab_order_item item ON item.id = link.order_item_id
  WHERE link.processing_id = p.id ORDER BY item.position LIMIT 1
);
ALTER TABLE processing ENABLE TRIGGER USER;

DELETE FROM audit_log WHERE entity_type = 'processing_order_item';
DROP TABLE processing_order_item;
