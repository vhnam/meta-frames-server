-- name: StatsCameras :many
SELECT c.id, (c.brand || ' ' || c.model)::text AS label, count(*)::int AS rolls
FROM roll r JOIN camera c ON c.id = r.camera_id
WHERE r.deleted_at IS NULL AND r.status <> 'in_stock'
  AND (sqlc.narg(year)::int IS NULL OR EXTRACT(YEAR FROM r.started_at) = sqlc.narg(year))
GROUP BY c.id ORDER BY rolls DESC, label;

-- name: StatsLenses :many
SELECT l.id, trim(COALESCE(l.brand, '') || ' ' || COALESCE(l.model, '') || ' ' || l.focal_length || 'mm f/' || l.max_aperture)::text AS label,
       count(*)::int AS rolls
FROM roll r JOIN roll_lens rl ON rl.roll_id = r.id JOIN lens l ON l.id = rl.lens_id
WHERE r.deleted_at IS NULL AND r.status <> 'in_stock'
  AND (sqlc.narg(year)::int IS NULL OR EXTRACT(YEAR FROM r.started_at) = sqlc.narg(year))
GROUP BY l.id ORDER BY rolls DESC, label;

-- name: StatsFilm :many
SELECT fs.id, (fs.brand || ' ' || fs.name)::text AS label, count(*)::int AS rolls
FROM roll r JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE r.deleted_at IS NULL AND r.status <> 'in_stock'
GROUP BY fs.id ORDER BY rolls DESC, label;

-- Groups stocks by their base stock (a stock without a base is its own group).
-- name: StatsFilmByBase :many
SELECT g.id, (g.brand || ' ' || g.name)::text AS label, count(*)::int AS rolls
FROM roll r
JOIN film_stock fs ON fs.id = r.film_stock_id
JOIN film_stock g ON g.id = COALESCE(fs.base_stock_id, fs.id)
WHERE r.deleted_at IS NULL AND r.status <> 'in_stock'
GROUP BY g.id ORDER BY rolls DESC, label;

-- name: StatsTimeline :many
SELECT EXTRACT(YEAR FROM started_at)::int AS year, EXTRACT(MONTH FROM started_at)::int AS month, count(*)::int AS rolls
FROM roll WHERE deleted_at IS NULL AND started_at IS NOT NULL
GROUP BY 1, 2 ORDER BY 1, 2;

-- Film cost is dated by when the roll was recorded (no purchase date is stored).
-- name: StatsFilmSpend :many
SELECT EXTRACT(YEAR FROM created_at)::int AS year, EXTRACT(MONTH FROM created_at)::int AS month,
       COALESCE(sum(price), 0)::int AS cost, (count(*) FILTER (WHERE price IS NULL))::int AS missing
FROM roll WHERE deleted_at IS NULL GROUP BY 1, 2;

-- name: StatsProcessingSpend :many
SELECT EXTRACT(YEAR FROM sent_at)::int AS year, EXTRACT(MONTH FROM sent_at)::int AS month,
       COALESCE(sum(price), 0)::int AS cost, (count(*) FILTER (WHERE price IS NULL))::int AS missing
FROM processing WHERE deleted_at IS NULL GROUP BY 1, 2;
