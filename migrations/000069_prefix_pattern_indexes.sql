-- +goose Up
-- B-tree prefix operators are collation-sensitive. Explicit pattern_ops
-- keeps the ordered prefix scans predictable on UTF-8 database locales.
CREATE INDEX IF NOT EXISTS native_maven_artifacts_visible_coordinate_pattern_idx
    ON native_maven_artifacts (repository_id, coordinate text_pattern_ops, build_number)
    WHERE state = 'visible';
CREATE INDEX IF NOT EXISTS native_oci_manifests_repository_name_pattern_idx
    ON native_oci_manifests (repository_id, name text_pattern_ops);
CREATE INDEX IF NOT EXISTS native_raw_assets_repository_path_pattern_idx
    ON native_raw_assets (repository_id, path text_pattern_ops);
CREATE INDEX IF NOT EXISTS native_conan_recipe_visible_reference_pattern_idx
    ON native_conan_recipe_revisions (repository_id, reference text_pattern_ops)
    WHERE state = 'visible';

-- +goose Down
DROP INDEX IF EXISTS native_conan_recipe_visible_reference_pattern_idx;
DROP INDEX IF EXISTS native_raw_assets_repository_path_pattern_idx;
DROP INDEX IF EXISTS native_oci_manifests_repository_name_pattern_idx;
DROP INDEX IF EXISTS native_maven_artifacts_visible_coordinate_pattern_idx;
