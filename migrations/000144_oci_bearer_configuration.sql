-- +goose Up
ALTER TABLE hosted_repositories
    ADD COLUMN IF NOT EXISTS oci_bearer JSONB;

-- +goose Down
-- Forward-only: dropping the column would discard configured issuer bindings.
