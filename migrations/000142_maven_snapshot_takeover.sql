-- +goose Up
ALTER TABLE native_maven_snapshot_imports
    ADD COLUMN takeover_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN takeover_actor TEXT NOT NULL DEFAULT '',
    ADD COLUMN taken_over_at TIMESTAMPTZ,
    ADD COLUMN current_build_number INTEGER NOT NULL DEFAULT 0 CHECK (current_build_number >= 0),
    ADD COLUMN current_aliases JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE native_maven_publish_sessions
    ADD COLUMN client_timestamp TEXT NOT NULL DEFAULT '',
    ADD COLUMN client_build_number INTEGER NOT NULL DEFAULT 0 CHECK (client_build_number >= 0);

DROP INDEX native_maven_open_publisher_coordinate_idx;
CREATE UNIQUE INDEX native_maven_open_publisher_coordinate_idx
    ON native_maven_publish_sessions(repository_id,coordinate,publisher)
    WHERE state='open' AND client_timestamp='';
CREATE UNIQUE INDEX native_maven_snapshot_client_receipt_idx
    ON native_maven_publish_sessions(repository_id,coordinate,publisher,client_timestamp,client_build_number)
    WHERE client_timestamp<>'';

ALTER TABLE native_maven_snapshot_imports ADD CONSTRAINT native_maven_takeover_receipt_consistent CHECK (
    (taken_over_at IS NULL AND takeover_key='' AND takeover_actor='' AND current_build_number=0)
    OR (taken_over_at IS NOT NULL AND state='committed' AND takeover_key<>'' AND takeover_actor<>''));
