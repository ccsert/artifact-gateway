-- +goose Up
CREATE TABLE IF NOT EXISTS repository_grant_sets (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS repository_grants (
    repository_id UUID NOT NULL REFERENCES repository_grant_sets(repository_id) ON DELETE CASCADE,
    principal TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    PRIMARY KEY (repository_id, principal)
);

-- +goose Down
-- Management aggregates are forward-only; compensate with a later migration.
