-- +goose Up
-- Four original formats already have literal LIKE/ILIKE trigram indexes.
-- Index the remaining search sources without changing canonical identities.
CREATE INDEX IF NOT EXISTS native_npm_packages_name_trgm_idx
    ON native_npm_packages USING gin (name gin_trgm_ops) WHERE NOT negative;
CREATE INDEX IF NOT EXISTS native_npm_versions_visible_name_trgm_idx
    ON native_npm_versions USING gin (package_name gin_trgm_ops) WHERE state='visible';
CREATE INDEX IF NOT EXISTS native_pypi_files_visible_project_trgm_idx
    ON native_pypi_files USING gin (project gin_trgm_ops) WHERE state='visible';
CREATE INDEX IF NOT EXISTS native_go_versions_module_path_trgm_idx
    ON native_go_versions USING gin (module_path gin_trgm_ops);
CREATE INDEX IF NOT EXISTS native_apt_assets_path_trgm_idx
    ON native_apt_assets USING gin (path gin_trgm_ops);
CREATE INDEX IF NOT EXISTS native_apt_snapshot_assets_path_trgm_idx
    ON native_apt_snapshot_assets USING gin (path gin_trgm_ops);
CREATE INDEX IF NOT EXISTS native_cargo_names_name_trgm_idx
    ON native_cargo_names USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS native_cargo_proxy_indexes_positive_name_trgm_idx
    ON native_cargo_proxy_indexes USING gin (name gin_trgm_ops) WHERE status=200;

-- +goose Down
DROP INDEX IF EXISTS native_cargo_proxy_indexes_positive_name_trgm_idx;
DROP INDEX IF EXISTS native_cargo_names_name_trgm_idx;
DROP INDEX IF EXISTS native_apt_snapshot_assets_path_trgm_idx;
DROP INDEX IF EXISTS native_apt_assets_path_trgm_idx;
DROP INDEX IF EXISTS native_go_versions_module_path_trgm_idx;
DROP INDEX IF EXISTS native_pypi_files_visible_project_trgm_idx;
DROP INDEX IF EXISTS native_npm_versions_visible_name_trgm_idx;
DROP INDEX IF EXISTS native_npm_packages_name_trgm_idx;
