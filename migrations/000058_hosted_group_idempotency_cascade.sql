-- +goose Up
ALTER TABLE hosted_group_idempotency DROP CONSTRAINT IF EXISTS hosted_group_idempotency_group_id_fkey;
ALTER TABLE hosted_group_idempotency
    ADD CONSTRAINT hosted_group_idempotency_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES hosted_groups(id) ON DELETE CASCADE;

-- +goose Down
-- Keep group cleanup cascading; removing it would make the delete API regress.
