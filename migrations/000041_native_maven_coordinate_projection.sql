-- +goose Up
CREATE INDEX IF NOT EXISTS native_maven_artifacts_visible_coordinate_idx
    ON native_maven_artifacts (repository_id, coordinate)
    WHERE state = 'visible';
