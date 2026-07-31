-- +goose Up
ALTER TABLE repository_grants ADD COLUMN IF NOT EXISTS resource_prefix TEXT NOT NULL DEFAULT '';
ALTER TABLE repository_grants DROP CONSTRAINT repository_grants_pkey;
ALTER TABLE repository_grants ADD PRIMARY KEY (repository_id, principal, resource_prefix);

-- +goose Down
-- Management aggregates are forward-only; compensate with a later migration.
