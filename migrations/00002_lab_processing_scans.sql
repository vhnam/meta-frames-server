-- +goose Up
CREATE TABLE lab (
  id         uuid PRIMARY KEY,
  name       text NOT NULL,
  address    text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- lab_id NULL means processed at home.
CREATE TABLE processing (
  id                    uuid PRIMARY KEY,
  roll_id               uuid NOT NULL REFERENCES roll(id),
  lab_id                uuid REFERENCES lab(id),
  type                  text NOT NULL CHECK (type IN ('develop', 'develop_scan', 'scan', 'print')),
  process               text NOT NULL CHECK (process IN ('C-41', 'E-6', 'BW', 'ECN-2')),
  sent_at               date NOT NULL,
  scans_received_at     date,
  negatives_returned_at date,
  price                 integer CHECK (price >= 0),
  notes                 text,
  created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX processing_roll_idx ON processing (roll_id);

CREATE TABLE frame (
  id      uuid PRIMARY KEY,
  roll_id uuid    NOT NULL REFERENCES roll(id),
  number  integer NOT NULL CHECK (number >= 0),
  notes   text,
  UNIQUE (roll_id, number)
);

CREATE TABLE scan (
  id            uuid PRIMARY KEY,
  processing_id uuid NOT NULL REFERENCES processing(id),
  frame_id      uuid NOT NULL REFERENCES frame(id),
  scanner       text NOT NULL CHECK (scanner IN ('noritsu', 'frontier', 'other')),
  file_key      text NOT NULL,
  file_name     text NOT NULL,
  content_type  text NOT NULL,
  size_bytes    bigint NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (processing_id, frame_id, scanner)
);
CREATE INDEX scan_frame_idx ON scan (frame_id);

CREATE TABLE idempotency_key (
  key        text PRIMARY KEY,
  status     integer,
  response   jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE idempotency_key;
DROP TABLE scan;
DROP TABLE frame;
DROP TABLE processing;
DROP TABLE lab;
