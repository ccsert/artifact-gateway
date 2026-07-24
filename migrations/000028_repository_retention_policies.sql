-- +goose Up
CREATE TABLE repository_retention_policies (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 1,
    keep_days INTEGER NOT NULL CHECK (keep_days >= 1),
    minimum_versions INTEGER NOT NULL CHECK (minimum_versions >= 1)
);
