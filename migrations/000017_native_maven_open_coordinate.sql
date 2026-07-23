-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS native_maven_open_coordinate_idx
    ON native_maven_publish_sessions(repository_id, coordinate)
    WHERE state = 'open';
