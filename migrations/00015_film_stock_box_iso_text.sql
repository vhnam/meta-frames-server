-- +goose Up
-- A film stock's box speed is a single ISO ("400") or a range ("100-400"), so it becomes text.
-- A roll's shot_iso stays an integer: a roll is shot at one speed.
ALTER TABLE film_stock DROP CONSTRAINT film_stock_box_iso_check;
ALTER TABLE film_stock ALTER COLUMN box_iso TYPE text USING box_iso::text;
ALTER TABLE film_stock ADD CONSTRAINT film_stock_box_iso_check
  CHECK (box_iso ~ '^[1-9][0-9]{0,5}(-[1-9][0-9]{0,5})?$');

-- +goose Down
-- A range keeps only its lower bound.
ALTER TABLE film_stock DROP CONSTRAINT film_stock_box_iso_check;
ALTER TABLE film_stock ALTER COLUMN box_iso TYPE integer USING split_part(box_iso, '-', 1)::integer;
ALTER TABLE film_stock ADD CONSTRAINT film_stock_box_iso_check CHECK (box_iso > 0);
