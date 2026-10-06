-- +goose Up
CREATE TABLE camera (
  id             uuid PRIMARY KEY,
  brand          text    NOT NULL,
  model          text    NOT NULL,
  mount          text,
  description    text,
  has_fixed_lens boolean NOT NULL DEFAULT false,
  is_active      boolean NOT NULL DEFAULT true,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (has_fixed_lens = (mount IS NULL))
);

CREATE TABLE lens (
  id           uuid PRIMARY KEY,
  brand        text,
  model        text,
  mount        text,
  description  text,
  focal_length integer       NOT NULL CHECK (focal_length > 0),
  max_aperture numeric(3, 1) NOT NULL CHECK (max_aperture > 0),
  is_built_in  boolean       NOT NULL DEFAULT false,
  is_active    boolean       NOT NULL DEFAULT true,
  created_at   timestamptz   NOT NULL DEFAULT now()
);

CREATE TABLE camera_lens (
  camera_id uuid NOT NULL REFERENCES camera(id),
  lens_id   uuid NOT NULL REFERENCES lens(id),
  PRIMARY KEY (camera_id, lens_id)
);

CREATE TABLE film_stock (
  id            uuid PRIMARY KEY,
  brand         text    NOT NULL,
  name          text    NOT NULL,
  type          text    NOT NULL CHECK (type IN ('color', 'bw', 'slide')),
  box_iso       integer NOT NULL CHECK (box_iso > 0),
  process       text    NOT NULL CHECK (process IN ('C-41', 'E-6', 'BW', 'ECN-2')),
  packaging     text    NOT NULL CHECK (packaging IN ('factory', 'repack', 'respooled')),
  stock_origin  text,
  pack_origin   text,
  description   text,
  base_stock_id uuid REFERENCES film_stock(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (base_stock_id IS NULL OR base_stock_id <> id)
);

CREATE TABLE roll (
  id            uuid PRIMARY KEY,
  film_stock_id uuid    NOT NULL REFERENCES film_stock(id),
  camera_id     uuid REFERENCES camera(id),
  format        integer NOT NULL,
  exposures     integer NOT NULL CHECK (exposures > 0),
  status        text    NOT NULL DEFAULT 'in_stock'
                CHECK (status IN ('in_stock', 'in_camera', 'done_shooting', 'at_lab', 'developed', 'scanned')),
  shot_iso      integer CHECK (shot_iso > 0),
  expiry_year   integer,
  expiry_month  integer CHECK (expiry_month BETWEEN 1 AND 12),
  price         integer CHECK (price >= 0),
  description   text,
  started_at    date,
  finished_at   date,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (expiry_month IS NULL OR expiry_year IS NOT NULL)
);
-- A camera holds at most one roll at a time.
CREATE UNIQUE INDEX roll_one_loaded_per_camera ON roll (camera_id) WHERE status = 'in_camera';
CREATE INDEX roll_film_stock_idx ON roll (film_stock_id);

CREATE TABLE roll_lens (
  roll_id uuid NOT NULL REFERENCES roll(id),
  lens_id uuid NOT NULL REFERENCES lens(id),
  PRIMARY KEY (roll_id, lens_id)
);

-- +goose Down
DROP TABLE roll_lens;
DROP TABLE roll;
DROP TABLE film_stock;
DROP TABLE camera_lens;
DROP TABLE lens;
DROP TABLE camera;
