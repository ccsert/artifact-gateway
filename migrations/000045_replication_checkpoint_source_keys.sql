-- +goose Up
ALTER TABLE replication_checkpoints ADD COLUMN source_object_key TEXT;
UPDATE replication_checkpoints SET source_object_key=object_key WHERE source_object_key IS NULL;
ALTER TABLE replication_checkpoints ALTER COLUMN source_object_key SET NOT NULL;

-- +goose Down
ALTER TABLE replication_checkpoints DROP COLUMN source_object_key;
