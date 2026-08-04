-- +goose Up
CREATE TABLE IF NOT EXISTS native_raw_asset_tombstones (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    path TEXT NOT NULL,
    digest TEXT NOT NULL,
    content_type TEXT NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, path)
);

CREATE INDEX IF NOT EXISTS native_raw_asset_tombstones_deleted_at_idx
    ON native_raw_asset_tombstones (deleted_at);

-- +goose Down
DROP TABLE IF EXISTS native_raw_asset_tombstones;
