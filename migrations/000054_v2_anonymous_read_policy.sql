-- +goose Up
ALTER TABLE hosted_repositories ADD COLUMN IF NOT EXISTS anonymous_read BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE hosted_groups ADD COLUMN IF NOT EXISTS anonymous_read BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
-- V2 management policy migrations are forward-only; compensate with a later migration.
