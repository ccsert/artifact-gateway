-- +goose Up
ALTER TABLE native_maven_publish_sessions
    ADD COLUMN IF NOT EXISTS publisher TEXT NOT NULL DEFAULT '';
DROP INDEX IF EXISTS native_maven_open_coordinate_idx;
CREATE UNIQUE INDEX IF NOT EXISTS native_maven_open_publisher_coordinate_idx
    ON native_maven_publish_sessions(repository_id, coordinate, publisher)
    WHERE state = 'open';
