-- name: ListLabOrders :many
SELECT o.*, l.name AS lab_name FROM lab_order o JOIN lab l ON l.id = o.lab_id
WHERE o.owner_id = $1 AND o.deleted_at IS NULL AND (sqlc.narg(lab_id)::uuid IS NULL OR o.lab_id = sqlc.narg(lab_id))
ORDER BY o.ordered_at DESC, o.code;

-- name: GetLabOrder :one
SELECT o.*, l.name AS lab_name FROM lab_order o JOIN lab l ON l.id = o.lab_id
WHERE o.id = $1 AND o.owner_id = $2 AND o.deleted_at IS NULL;

-- name: GetLabOrderForUpdate :one
SELECT * FROM lab_order WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE;

-- name: InsertLabOrder :one
INSERT INTO lab_order (id, owner_id, lab_id, code, ordered_at, due_date, vat, discount, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: UpdateLabOrder :one
UPDATE lab_order SET lab_id = $3, code = $4, ordered_at = $5, due_date = $6, vat = $7, discount = $8, notes = $9
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteLabOrder :execrows
UPDATE lab_order SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: LabOrderHasJobs :one
SELECT EXISTS (
  SELECT 1 FROM processing p JOIN lab_order_item item ON item.id = p.order_item_id
  WHERE item.order_id = $1 AND p.owner_id = $2 AND p.deleted_at IS NULL
);

-- name: ListLabOrderItems :many
SELECT item.*, cost.unit_cost FROM lab_order_item item JOIN lab_order_item_cost cost ON cost.item_id = item.id
WHERE item.owner_id = $1 AND item.order_id = ANY(sqlc.arg(order_ids)::uuid[])
ORDER BY item.order_id, item.position;

-- name: GetLabOrderItemForUpdate :one
SELECT item.*, o.lab_id FROM lab_order_item item JOIN lab_order o ON o.id = item.order_id
WHERE item.id = $1 AND item.owner_id = $2 AND o.deleted_at IS NULL FOR UPDATE OF item;

-- A line's id is chosen by the client, so one that names a line of another order changes nothing.
-- name: UpsertLabOrderItem :execrows
INSERT INTO lab_order_item (id, owner_id, order_id, product, options, quantity, unit_price, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET product = EXCLUDED.product, options = EXCLUDED.options, quantity = EXCLUDED.quantity,
  unit_price = EXCLUDED.unit_price, position = EXCLUDED.position
WHERE lab_order_item.order_id = EXCLUDED.order_id AND lab_order_item.owner_id = EXCLUDED.owner_id;

-- name: DeleteLabOrderItem :execrows
DELETE FROM lab_order_item WHERE id = $1 AND owner_id = $2;

-- name: CountLabOrderItemJobs :one
SELECT count(*)::int FROM processing WHERE order_item_id = $1 AND owner_id = $2 AND deleted_at IS NULL AND id <> sqlc.arg(except_job_id)::uuid;

-- name: ListLabOrderJobs :many
SELECT item.order_id, p.order_item_id::uuid AS order_item_id, p.id, p.roll_id, p.type, p.sent_at, r.format, r.exposures,
       fs.brand AS stock_brand, fs.name AS stock_name
FROM processing p
JOIN lab_order_item item ON item.id = p.order_item_id
JOIN roll r ON r.id = p.roll_id
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE p.owner_id = $1 AND p.deleted_at IS NULL AND item.order_id = ANY(sqlc.arg(order_ids)::uuid[])
ORDER BY p.sent_at, p.created_at;

-- Jobs at a lab that are on no order yet, newest first.
-- name: ListLinkableJobs :many
SELECT p.id, p.roll_id, p.type, p.sent_at, r.format, r.exposures, fs.brand AS stock_brand, fs.name AS stock_name
FROM processing p
JOIN roll r ON r.id = p.roll_id
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE p.owner_id = $1 AND p.lab_id = $2 AND p.order_item_id IS NULL AND p.deleted_at IS NULL AND r.deleted_at IS NULL
ORDER BY p.sent_at DESC, p.created_at DESC;

-- A job on an order takes its price from the line, so its own price goes.
-- name: SetProcessingOrderItem :one
UPDATE processing SET order_item_id = $3, price = NULL
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL RETURNING *;
