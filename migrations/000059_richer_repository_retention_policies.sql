-- +goose Up
ALTER TABLE repository_retention_policies
    ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS snapshot_keep_days INTEGER NOT NULL DEFAULT 30 CHECK (snapshot_keep_days >= 1),
    ADD COLUMN IF NOT EXISTS maximum_versions INTEGER NOT NULL DEFAULT 0 CHECK (maximum_versions >= 0),
    ADD COLUMN IF NOT EXISTS coordinate_patterns TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS protected_patterns TEXT[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'repository_retention_versions_valid'
          AND conrelid = 'repository_retention_policies'::regclass
    ) THEN
        ALTER TABLE repository_retention_policies
            ADD CONSTRAINT repository_retention_versions_valid
            CHECK (maximum_versions = 0 OR maximum_versions >= minimum_versions);
    END IF;
END $$;
