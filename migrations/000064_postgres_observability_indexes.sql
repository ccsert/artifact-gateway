-- +goose Up
-- pg_stat_statements requires shared_preload_libraries=pg_stat_statements in
-- the PostgreSQL service. The extension itself is kept in the migration so
-- fresh and existing databases converge on the same observability surface.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Append-only timelines benefit from BRIN indexes while keeping index storage
-- bounded as audit and background-job tables grow.
CREATE INDEX IF NOT EXISTS resolver_audit_log_occurred_at_brin_idx
    ON resolver_audit_log USING brin (occurred_at) WITH (pages_per_range = 128);
CREATE INDEX IF NOT EXISTS lifecycle_jobs_created_at_brin_idx
    ON lifecycle_jobs USING brin (created_at) WITH (pages_per_range = 128);
CREATE INDEX IF NOT EXISTS replication_checkpoints_updated_at_brin_idx
    ON replication_checkpoints USING brin (updated_at) WITH (pages_per_range = 128);
CREATE INDEX IF NOT EXISTS native_oci_manifests_created_at_brin_idx
    ON native_oci_manifests USING brin (created_at) WITH (pages_per_range = 128);
CREATE INDEX IF NOT EXISTS native_conan_recipe_revisions_created_at_brin_idx
    ON native_conan_recipe_revisions USING brin (created_at) WITH (pages_per_range = 128);

-- Search endpoints accept a user-visible prefix or revision fragment. Keep
-- the existing btree indexes for ordered pagination and add trigram indexes
-- for non-prefix filters such as digest or long coordinates.
CREATE INDEX IF NOT EXISTS native_maven_artifacts_visible_coordinate_trgm_idx
    ON native_maven_artifacts USING gin (coordinate gin_trgm_ops)
    WHERE state = 'visible';
CREATE INDEX IF NOT EXISTS native_oci_manifests_name_trgm_idx
    ON native_oci_manifests USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS native_conan_recipe_revisions_visible_reference_trgm_idx
    ON native_conan_recipe_revisions USING gin (reference gin_trgm_ops)
    WHERE state = 'visible';
CREATE INDEX IF NOT EXISTS native_raw_assets_path_trgm_idx
    ON native_raw_assets USING gin (path gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS native_raw_assets_path_trgm_idx;
DROP INDEX IF EXISTS native_conan_recipe_revisions_visible_reference_trgm_idx;
DROP INDEX IF EXISTS native_oci_manifests_name_trgm_idx;
DROP INDEX IF EXISTS native_maven_artifacts_visible_coordinate_trgm_idx;
DROP INDEX IF EXISTS native_conan_recipe_revisions_created_at_brin_idx;
DROP INDEX IF EXISTS native_oci_manifests_created_at_brin_idx;
DROP INDEX IF EXISTS replication_checkpoints_updated_at_brin_idx;
DROP INDEX IF EXISTS lifecycle_jobs_created_at_brin_idx;
DROP INDEX IF EXISTS resolver_audit_log_occurred_at_brin_idx;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pg_stat_statements;
