-- +goose Up
-- Keep local build sequence/cursors stable. Imported filenames use their own
-- complete source identity, which may reuse a source build number.
ALTER TABLE native_maven_artifacts ADD COLUMN IF NOT EXISTS source_timestamp TEXT NOT NULL DEFAULT '';
ALTER TABLE native_maven_artifacts ADD COLUMN IF NOT EXISTS source_build_number INT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS native_maven_source_snapshot_identity
 ON native_maven_artifacts(repository_id,coordinate,source_timestamp,source_build_number)
 WHERE source_timestamp <> '';
CREATE TABLE IF NOT EXISTS native_maven_snapshot_imports (
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id), coordinate TEXT NOT NULL,
 target_id TEXT NOT NULL, target_binding TEXT NOT NULL, source_id TEXT NOT NULL, manifest_digest TEXT NOT NULL, plan_digest TEXT NOT NULL,
 session_id UUID NOT NULL REFERENCES native_maven_publish_sessions(id), actor TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('staged','committed')), metadata JSONB, aliases JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(repository_id,coordinate)
);
