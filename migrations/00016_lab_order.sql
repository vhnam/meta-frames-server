-- +goose Up
-- A lab bills rolls in batches, so what a job costs comes from the order the lab issued. An order
-- has lines ("35mm Colour film x 3 at 145,000"), and a job points at one line; a line takes up to
-- its quantity of jobs, which the server checks. "order" is an SQL keyword, hence lab_order.
CREATE TABLE lab_order (
  id         uuid PRIMARY KEY,
  owner_id   uuid REFERENCES app_user(id),
  lab_id     uuid NOT NULL,
  code       text NOT NULL CHECK (btrim(code) <> ''),
  ordered_at timestamptz NOT NULL,
  due_date   date,
  vat        integer NOT NULL DEFAULT 0 CHECK (vat >= 0),
  discount   integer NOT NULL DEFAULT 0 CHECK (discount >= 0),
  notes      text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  CONSTRAINT lab_order_id_owner UNIQUE (id, owner_id),
  CONSTRAINT lab_order_lab_same_owner FOREIGN KEY (lab_id, owner_id) REFERENCES lab (id, owner_id)
);
-- A lab's order codes are unique within the account; another lab may reuse a code.
CREATE UNIQUE INDEX lab_order_code_live_idx ON lab_order (owner_id, lab_id, lower(code)) WHERE deleted_at IS NULL;
CREATE INDEX lab_order_owner_idx ON lab_order (owner_id, ordered_at DESC);

-- Rows are replaced as a set by PUT /lab-orders/{id}; removals are real deletes (the audit trigger
-- keeps the history). A deleted job lets go of its line, so a line in use always has a live job.
CREATE TABLE lab_order_item (
  id         uuid PRIMARY KEY,
  owner_id   uuid REFERENCES app_user(id),
  order_id   uuid NOT NULL,
  product    text NOT NULL CHECK (btrim(product) <> ''),
  options    text,
  quantity   integer NOT NULL CHECK (quantity > 0),
  unit_price integer NOT NULL CHECK (unit_price >= 0),
  position   integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT lab_order_item_id_owner UNIQUE (id, owner_id),
  CONSTRAINT lab_order_item_order_same_owner FOREIGN KEY (order_id, owner_id) REFERENCES lab_order (id, owner_id)
);
CREATE INDEX lab_order_item_order_idx ON lab_order_item (order_id, position);

ALTER TABLE processing ADD COLUMN order_item_id uuid;
ALTER TABLE processing ADD CONSTRAINT processing_order_item_same_owner
  FOREIGN KEY (order_item_id, owner_id) REFERENCES lab_order_item (id, owner_id);
CREATE INDEX processing_order_item_idx ON processing (order_item_id);

-- What one unit of a line costs once the order's VAT and discount are shared out by price. It is
-- rounded to the dong, so the units of an order can add up to a few dong off its total.
CREATE VIEW lab_order_item_cost AS
SELECT item.id AS item_id,
       COALESCE(round(item.unit_price + item.unit_price * (o.vat - o.discount)::numeric / NULLIF(lines.subtotal, 0)), 0)::int AS unit_cost
FROM lab_order_item item
JOIN lab_order o ON o.id = item.order_id
JOIN (SELECT order_id, sum(quantity::bigint * unit_price) AS subtotal FROM lab_order_item GROUP BY order_id) AS lines
  ON lines.order_id = item.order_id;

CREATE TRIGGER lab_order_set_updated_at BEFORE UPDATE ON lab_order FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER lab_order_item_set_updated_at BEFORE UPDATE ON lab_order_item FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER lab_order_audit AFTER INSERT OR UPDATE OR DELETE ON lab_order FOR EACH ROW EXECUTE FUNCTION audit_row('id');
CREATE TRIGGER lab_order_item_audit AFTER INSERT OR UPDATE OR DELETE ON lab_order_item FOR EACH ROW EXECUTE FUNCTION audit_row('id');

-- +goose Down
DROP VIEW lab_order_item_cost;
ALTER TABLE processing DROP COLUMN order_item_id;
DELETE FROM audit_log WHERE entity_type IN ('lab_order', 'lab_order_item');
DROP TABLE lab_order_item;
DROP TABLE lab_order;
