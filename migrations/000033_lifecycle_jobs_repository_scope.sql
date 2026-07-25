-- +goose Up
ALTER TABLE lifecycle_jobs ALTER COLUMN repository_id SET NOT NULL;
