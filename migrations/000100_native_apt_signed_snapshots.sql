-- +goose Up
ALTER TABLE native_apt_repository_snapshots
    ADD COLUMN IF NOT EXISTS signature_algorithm TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS native_apt_snapshot_assets (
    snapshot_id UUID NOT NULL REFERENCES native_apt_repository_snapshots(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    path TEXT NOT NULL CHECK (path <> '' AND length(path) <= 2048 AND path !~ '(^/|(^|/)\.\.?(/|$)|[\\\r\n\t?#])'),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key = 'native/apt/sha256/' || substr(digest,8)),
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 1073741824),
    content_type TEXT NOT NULL CHECK (content_type <> '' AND length(content_type) <= 255),
    PRIMARY KEY (snapshot_id, path)
);
CREATE INDEX IF NOT EXISTS native_apt_snapshot_assets_visible_lookup_idx
    ON native_apt_snapshot_assets (repository_id, path, snapshot_id);

-- +goose Down
DROP TABLE IF EXISTS native_apt_snapshot_assets;
ALTER TABLE native_apt_repository_snapshots
    DROP COLUMN IF EXISTS signature_algorithm;
