-- +goose Up
-- Empty versions are unverified cache identities. Readers must fetch using the
-- current repository credentials before accepting these preexisting rows.
ALTER TABLE native_npm_packages ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE native_pypi_files ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE native_cargo_proxy_configs ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE native_cargo_proxy_indexes ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE native_cargo_proxy_crates ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE native_apt_assets ADD COLUMN source_version TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Forward-only: cache identities must not revert to trusting old credentials.
