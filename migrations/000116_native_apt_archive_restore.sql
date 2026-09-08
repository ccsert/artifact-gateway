-- +goose Up
CREATE TABLE native_apt_archive_restores (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 snapshot_id UUID NOT NULL,
 plan_digest TEXT NOT NULL CHECK (plan_digest ~ '^sha256:[0-9a-f]{64}$'),
 state TEXT NOT NULL CHECK (state IN ('preparing','completed','failed')),
 reserved_bytes BIGINT NOT NULL CHECK (reserved_bytes >= 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 finished_at TIMESTAMPTZ,
 UNIQUE (id,repository_id)
);
CREATE UNIQUE INDEX native_apt_archive_restores_active_idx ON native_apt_archive_restores(snapshot_id) WHERE state='preparing';
CREATE INDEX native_apt_archive_restores_expiry_idx ON native_apt_archive_restores(created_at,id) WHERE state='preparing';
CREATE TABLE native_apt_archive_object_intents (
 restore_id UUID NOT NULL,
 repository_id UUID NOT NULL,
 object_key TEXT NOT NULL,
 digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK (size>0 AND size<=1073741824),
 reclaim_scheduled_at TIMESTAMPTZ,
 collected_at TIMESTAMPTZ,
 PRIMARY KEY (restore_id,object_key),
 FOREIGN KEY (restore_id,repository_id) REFERENCES native_apt_archive_restores(id,repository_id) ON DELETE CASCADE,
 CHECK (object_key='native/apt/sha256/'||substr(digest,8))
);
CREATE INDEX native_apt_archive_object_intents_reclaim_idx ON native_apt_archive_object_intents(restore_id,object_key) WHERE reclaim_scheduled_at IS NULL AND collected_at IS NULL;
CREATE INDEX native_apt_archive_object_intents_object_idx ON native_apt_archive_object_intents(object_key);
-- +goose Down
DROP TABLE native_apt_archive_object_intents;
DROP TABLE native_apt_archive_restores;
