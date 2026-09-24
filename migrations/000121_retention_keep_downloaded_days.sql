-- +goose Up
ALTER TABLE repository_retention_policies
    ADD COLUMN IF NOT EXISTS keep_downloaded_days INTEGER NOT NULL DEFAULT 0;
