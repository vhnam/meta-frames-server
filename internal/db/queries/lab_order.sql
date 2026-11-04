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
  SELECT 1 FROM processing_order_item link
  JOIN processing p ON p.id = link.processing_id
  JOIN lab_order_item item ON item.id = link.order_item_id
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
SELECT count(*)::int FROM processing_order_item link JOIN processing p ON p.id = link.processing_id
WHERE link.order_item_id = $1 AND p.owner_id = $2 AND p.deleted_at IS NULL AND p.id <> sqlc.arg(except_job_id)::uuid;

-- name: ListLabOrderJobs :many
SELECT item.order_id, link.order_item_id, p.id, p.roll_id, p.type, p.sent_at, r.format, r.exposures,
       fs.brand AS stock_brand, fs.name AS stock_name
FROM processing_order_item link
JOIN processing p ON p.id = link.processing_id
JOIN lab_order_item item ON item.id = link.order_item_id
JOIN roll r ON r.id = p.roll_id
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE p.owner_id = $1 AND p.deleted_at IS NULL AND item.order_id = ANY(sqlc.arg(order_ids)::uuid[])
ORDER BY p.sent_at, p.created_at;

-- Jobs at a lab that are on no order, or only on this one, each with the lines it already has.
-- name: ListLinkableJobs :many
SELECT p.id, p.roll_id, p.type, p.sent_at, r.format, r.exposures, fs.brand AS stock_brand, fs.name AS stock_name,
       ARRAY(SELECT link.order_item_id FROM processing_order_item link WHERE link.processing_id = p.id)::uuid[] AS order_item_ids
FROM processing p
JOIN roll r ON r.id = p.roll_id
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE p.owner_id = $1 AND p.lab_id = $2 AND p.deleted_at IS NULL AND r.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM processing_order_item link JOIN lab_order_item item ON item.id = link.order_item_id
    WHERE link.processing_id = p.id AND item.order_id <> sqlc.arg(order_id)::uuid
  )
ORDER BY p.sent_at DESC, p.created_at DESC;

-- name: ListJobOrderItems :many
SELECT link.processing_id, item.id AS item_id, item.product, o.id AS order_id, o.code AS order_code
FROM processing_order_item link
JOIN lab_order_item item ON item.id = link.order_item_id
JOIN lab_order o ON o.id = item.order_id
WHERE item.owner_id = $1 AND link.processing_id = ANY(sqlc.arg(processing_ids)::uuid[])
ORDER BY link.processing_id, item.position;

-- name: AddJobOrderItem :exec
INSERT INTO processing_order_item (processing_id, order_item_id) VALUES ($1, $2)
ON CONFLICT (processing_id, order_item_id) DO NOTHING;

-- name: DeleteJobOrderItemsExcept :exec
DELETE FROM processing_order_item WHERE processing_id = $1 AND NOT (order_item_id = ANY(@keep::uuid[]));

-- A job on an order takes its price from its lines, so its own price goes.
-- name: ClearProcessingPrice :exec
UPDATE processing SET price = NULL WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;
