-- +goose Up
ALTER TABLE native_apt_repository_snapshots
    ADD CONSTRAINT native_apt_repository_snapshots_id_repository_unique
    UNIQUE (id, repository_id);
ALTER TABLE native_apt_snapshot_assets
    DROP CONSTRAINT native_apt_snapshot_assets_snapshot_id_fkey,
    ADD CONSTRAINT native_apt_snapshot_assets_snapshot_repository_fk
        FOREIGN KEY (snapshot_id, repository_id)
        REFERENCES native_apt_repository_snapshots(id, repository_id)
        ON DELETE CASCADE,
    ADD CONSTRAINT native_apt_snapshot_assets_path_root_check
        CHECK (path ~ '^(dists|pool)/');

-- +goose Down
ALTER TABLE native_apt_snapshot_assets
    DROP CONSTRAINT IF EXISTS native_apt_snapshot_assets_path_root_check,
    DROP CONSTRAINT IF EXISTS native_apt_snapshot_assets_snapshot_repository_fk,
    ADD CONSTRAINT native_apt_snapshot_assets_snapshot_id_fkey
        FOREIGN KEY (snapshot_id) REFERENCES native_apt_repository_snapshots(id) ON DELETE CASCADE;
ALTER TABLE native_apt_repository_snapshots
    DROP CONSTRAINT IF EXISTS native_apt_repository_snapshots_id_repository_unique;
