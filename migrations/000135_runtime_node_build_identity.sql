-- +goose Up
ALTER TABLE runtime_node_sessions
    ADD COLUMN build_version TEXT,
    ADD COLUMN build_revision TEXT;

ALTER TABLE runtime_nodes
    ADD COLUMN build_version TEXT,
    ADD COLUMN build_revision TEXT;

-- +goose Down
-- Forward-only: preserve recorded build identity for rolling upgrades.
