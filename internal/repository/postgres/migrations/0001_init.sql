-- pg_trgm powers "did you mean" suggestions for misspelled tracker names.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE trackers (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    description TEXT        NOT NULL DEFAULT '',
    created_by  TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

-- Only one *active* tracker per name (case-insensitive). Deleted trackers free their name.
CREATE UNIQUE INDEX trackers_active_name_uq ON trackers (lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX trackers_name_trgm_idx ON trackers USING gin (lower(name) gin_trgm_ops);

CREATE TABLE tracker_columns (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tracker_id    BIGINT      NOT NULL REFERENCES trackers (id) ON DELETE CASCADE,
    name          TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    type          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    required      BOOLEAN     NOT NULL DEFAULT false,
    options       JSONB       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(options) = 'array'),
    default_value JSONB,
    position      INT         NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX tracker_columns_name_uq ON tracker_columns (tracker_id, lower(name));

-- Row values are keyed by tracker_columns.id (as text), never by user supplied names.
CREATE TABLE tracker_rows (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tracker_id BIGINT      NOT NULL REFERENCES trackers (id) ON DELETE CASCADE,
    data       JSONB       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object'),
    created_by TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tracker_rows_tracker_id_idx ON tracker_rows (tracker_id, id);
-- Speeds up exact-match filters (select options, numbers, booleans) expressed as data @> {...}.
CREATE INDEX tracker_rows_data_gin_idx ON tracker_rows USING gin (data jsonb_path_ops);
