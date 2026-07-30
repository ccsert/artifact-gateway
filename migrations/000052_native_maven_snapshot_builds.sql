-- +goose Up
-- Maven SNAPSHOT coordinates become multi-build: every publish of the same
-- -SNAPSHOT coordinate allocates an incrementing build_number while releases
-- keep build_number 0 and stay immutable. The coordinate uniqueness therefore
-- moves from (repository_id, coordinate) to (repository_id, coordinate,
-- build_number). All statements are idempotent because the migrate container
-- replays every migration on each start.
ALTER TABLE native_maven_artifacts ADD COLUMN IF NOT EXISTS build_number INT NOT NULL DEFAULT 0;

-- Existing -SNAPSHOT rows were the first (and only) build of their coordinate.
UPDATE native_maven_artifacts
SET build_number = 1
WHERE build_number = 0 AND split_part(coordinate, ':', 3) LIKE '%-SNAPSHOT';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'native_maven_artifacts_repository_id_coordinate_key') THEN
        ALTER TABLE native_maven_artifacts DROP CONSTRAINT native_maven_artifacts_repository_id_coordinate_key;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS native_maven_artifacts_repository_coordinate_build_key
    ON native_maven_artifacts (repository_id, coordinate, build_number);

DROP INDEX IF EXISTS native_maven_artifacts_visible_coordinate_idx;
CREATE INDEX native_maven_artifacts_visible_coordinate_idx
    ON native_maven_artifacts (repository_id, coordinate, build_number)
    WHERE state = 'visible';
