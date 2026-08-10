-- +goose Up
ALTER TABLE lifecycle_jobs DROP CONSTRAINT IF EXISTS lifecycle_jobs_kind_check;
ALTER TABLE lifecycle_jobs ADD CONSTRAINT lifecycle_jobs_kind_check CHECK (kind IN ('retention', 'promotion', 'replication', 'reclaim', 'intelligence', 'scan'));

-- +goose Down
ALTER TABLE lifecycle_jobs DROP CONSTRAINT IF EXISTS lifecycle_jobs_kind_check;
ALTER TABLE lifecycle_jobs ADD CONSTRAINT lifecycle_jobs_kind_check CHECK (kind IN ('retention', 'promotion', 'replication', 'reclaim', 'intelligence'));
