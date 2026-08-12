-- +goose Up
ALTER TABLE native_apt_publication_sessions
    ADD COLUMN IF NOT EXISTS reclaim_scheduled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS collected_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS native_apt_publication_sessions_collection_idx
    ON native_apt_publication_sessions (expires_at, id)
    WHERE state='aborted' AND object_key<>'' AND collected_at IS NULL;
CREATE INDEX IF NOT EXISTS native_apt_publication_sessions_reclaim_schedule_idx
    ON native_apt_publication_sessions (expires_at, id)
    WHERE state='aborted' AND object_key<>'' AND reclaim_scheduled_at IS NULL AND collected_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS native_apt_publication_sessions_collection_idx;
DROP INDEX IF EXISTS native_apt_publication_sessions_reclaim_schedule_idx;
ALTER TABLE native_apt_publication_sessions
    DROP COLUMN IF EXISTS collected_at,
    DROP COLUMN IF EXISTS reclaim_scheduled_at;
