-- +goose Up
CREATE TABLE IF NOT EXISTS native_apt_pool_paths (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    path TEXT NOT NULL CHECK (path ~ '^pool/' AND length(path) <= 2048),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key = 'native/apt/sha256/' || substr(digest,8)),
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 1073741824),
    content_type TEXT NOT NULL CHECK (content_type <> '' AND length(content_type) <= 255),
    PRIMARY KEY (repository_id, path)
);

CREATE TABLE IF NOT EXISTS native_apt_snapshot_object_intents (
    snapshot_id UUID NOT NULL,
    repository_id UUID NOT NULL,
    object_key TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 1073741824),
    reclaim_scheduled_at TIMESTAMPTZ,
    collected_at TIMESTAMPTZ,
    PRIMARY KEY (snapshot_id, object_key),
    FOREIGN KEY (snapshot_id, repository_id)
        REFERENCES native_apt_repository_snapshots(id, repository_id)
        ON DELETE CASCADE,
    CHECK (object_key = 'native/apt/sha256/' || substr(digest,8))
);
CREATE INDEX IF NOT EXISTS native_apt_snapshot_object_intents_reclaim_idx
    ON native_apt_snapshot_object_intents (snapshot_id, object_key)
    WHERE reclaim_scheduled_at IS NULL AND collected_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS native_apt_snapshot_object_intents;
DROP TABLE IF EXISTS native_apt_pool_paths;
