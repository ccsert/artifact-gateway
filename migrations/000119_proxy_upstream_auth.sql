-- +goose Up
ALTER TABLE hosted_repositories
    ADD COLUMN IF NOT EXISTS upstream_auth JSONB;

-- +goose Down
-- Forward-only: dropping the column would discard encrypted upstream credentials.
