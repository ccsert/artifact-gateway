-- +goose Up
ALTER TABLE native_apt_repository_snapshots ADD COLUMN retired_at TIMESTAMPTZ;
-- Existing retirement times are unknown. Start a fresh full grace period.
UPDATE native_apt_repository_snapshots SET retired_at=now() WHERE state='retired';
ALTER TABLE native_apt_repository_snapshots DROP CONSTRAINT native_apt_repository_snapshots_state_check;
ALTER TABLE native_apt_repository_snapshots ADD CONSTRAINT native_apt_repository_snapshots_state_check CHECK (state IN ('building','signed','visible','retired','failed','pruned'));
ALTER TABLE native_apt_repository_snapshots DROP CONSTRAINT native_apt_repository_snapshots_check;
ALTER TABLE native_apt_repository_snapshots ADD CONSTRAINT native_apt_repository_snapshots_check CHECK ((state IN ('building','signed','failed') AND published_at IS NULL) OR (state IN ('visible','retired','pruned') AND published_at IS NOT NULL));
CREATE TABLE native_apt_lifecycle_results (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 request_digest TEXT NOT NULL,
 snapshot_id UUID NOT NULL REFERENCES native_apt_repository_snapshots(id)
);
CREATE TABLE native_apt_package_deletions (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 suite TEXT NOT NULL,
 data JSONB NOT NULL,
 reclaim_scheduled_at TIMESTAMPTZ,
 collected_at TIMESTAMPTZ
);
CREATE INDEX native_apt_package_deletions_repository_idx ON native_apt_package_deletions(repository_id,suite);
ALTER TABLE native_apt_snapshot_assets DROP CONSTRAINT native_apt_snapshot_assets_size_check;
ALTER TABLE native_apt_snapshot_assets ADD CONSTRAINT native_apt_snapshot_assets_size_check CHECK (size>=0 AND size<=1073741824 AND (size>0 OR digest='sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'));
ALTER TABLE native_apt_snapshot_object_intents DROP CONSTRAINT native_apt_snapshot_object_intents_size_check;
ALTER TABLE native_apt_snapshot_object_intents ADD CONSTRAINT native_apt_snapshot_object_intents_size_check CHECK (size>=0 AND size<=1073741824 AND (size>0 OR digest='sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'));
ALTER TABLE native_apt_archive_object_intents DROP CONSTRAINT native_apt_archive_object_intents_size_check;
ALTER TABLE native_apt_archive_object_intents ADD CONSTRAINT native_apt_archive_object_intents_size_check CHECK (size>=0 AND size<=1073741824 AND (size>0 OR digest='sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'));
-- Downgrade must restore a pre-lifecycle backup: old binaries cannot safely
-- interpret deletion barriers, empty indices, or pruned snapshot history.
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'APT lifecycle requires backup restoration for downgrade'; END $$;
-- +goose StatementEnd
