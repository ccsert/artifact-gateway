-- +goose Up
CREATE INDEX IF NOT EXISTS artifact_usage_stats_repository_address_idx
    ON artifact_usage_stats (repository, resource COLLATE "C", format COLLATE "C");
