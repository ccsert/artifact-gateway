-- +goose Up
ALTER TABLE hosted_repositories
    ADD COLUMN IF NOT EXISTS egress_proxy JSONB;

-- +goose Down
-- Forward-only: dropping the column would discard encrypted egress credentials.
