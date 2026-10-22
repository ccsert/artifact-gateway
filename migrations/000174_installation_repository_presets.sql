-- +goose Up
CREATE TABLE installation_repository_presets (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    catalog_version TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'skipped_existing')),
    repositories_created INTEGER NOT NULL DEFAULT 0 CHECK (repositories_created >= 0),
    groups_created INTEGER NOT NULL DEFAULT 0 CHECK (groups_created >= 0),
    finished_at TIMESTAMPTZ
);

-- Record the upgrade boundary before the new runtime starts. Administrators'
-- existing catalogs, including deleted repository records, are preserved.
INSERT INTO installation_repository_presets (singleton, catalog_version, state, finished_at)
SELECT true, 'standard-v1',
    CASE WHEN EXISTS(SELECT 1 FROM hosted_repositories) OR EXISTS(SELECT 1 FROM hosted_groups)
         THEN 'skipped_existing' ELSE 'pending' END,
    CASE WHEN EXISTS(SELECT 1 FROM hosted_repositories) OR EXISTS(SELECT 1 FROM hosted_groups)
         THEN now() ELSE NULL END;
