-- name: GetFilmStock :one
SELECT * FROM film_stock WHERE id = $1 AND deleted_at IS NULL;

-- name: GetFilmStockForUpdate :one
SELECT * FROM film_stock WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ListFilmStocks :many
SELECT * FROM film_stock
WHERE deleted_at IS NULL AND (sqlc.narg(q)::text IS NULL
       OR brand ILIKE '%' || sqlc.narg(q) || '%' OR name ILIKE '%' || sqlc.narg(q) || '%')
ORDER BY brand, name;

-- name: InsertFilmStock :one
INSERT INTO film_stock (id, brand, name, type, box_iso, process, packaging,
                        stock_origin, pack_origin, description, base_stock_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateFilmStock :one
UPDATE film_stock SET brand = $2, name = $3, type = $4, box_iso = $5, process = $6,
       packaging = $7, stock_origin = $8, pack_origin = $9, description = $10,
       base_stock_id = $11
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListSiblingStocks :many
SELECT * FROM film_stock
WHERE base_stock_id = $1 AND id <> $2 AND deleted_at IS NULL
ORDER BY brand, name;

-- name: ListDerivedStocks :many
SELECT * FROM film_stock WHERE base_stock_id = $1 AND deleted_at IS NULL ORDER BY brand, name;

-- name: InventoryRows :many
SELECT fs.id AS stock_id, r.format, count(*)::int AS roll_count
FROM roll r JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE r.status = 'in_stock' AND r.deleted_at IS NULL
  AND (sqlc.narg(type)::text IS NULL OR fs.type = sqlc.narg(type))
  AND (sqlc.narg(process)::text IS NULL OR fs.process = sqlc.narg(process))
  AND (sqlc.narg(iso)::int IS NULL OR fs.box_iso = sqlc.narg(iso))
GROUP BY fs.id, r.format
ORDER BY fs.id, r.format;

-- name: GetFilmStocksByIDs :many
SELECT * FROM film_stock WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL ORDER BY brand, name;

-- Soonest expiry per in-stock stock; an unknown month sorts as December but is returned as NULL.
-- name: SoonestExpiries :many
SELECT DISTINCT ON (fs.id) fs.id AS stock_id, r.expiry_year, r.expiry_month
FROM roll r JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE r.status = 'in_stock' AND r.deleted_at IS NULL AND r.expiry_year IS NOT NULL
  AND (sqlc.narg(type)::text IS NULL OR fs.type = sqlc.narg(type))
  AND (sqlc.narg(process)::text IS NULL OR fs.process = sqlc.narg(process))
  AND (sqlc.narg(iso)::int IS NULL OR fs.box_iso = sqlc.narg(iso))
ORDER BY fs.id, r.expiry_year, COALESCE(r.expiry_month, 12);

-- name: FilmStockInUse :one
SELECT EXISTS (
    SELECT 1 FROM roll WHERE film_stock_id = $1 AND deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM film_stock WHERE base_stock_id = $1 AND deleted_at IS NULL
);

-- name: SoftDeleteFilmStock :execrows
UPDATE film_stock SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
