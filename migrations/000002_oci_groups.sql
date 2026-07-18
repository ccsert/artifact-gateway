-- +goose Up
CREATE TABLE IF NOT EXISTS oci_groups (
    name TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oci_group_members (
    group_name TEXT NOT NULL REFERENCES oci_groups(name) ON DELETE CASCADE,
    name TEXT NOT NULL,
    member_type TEXT NOT NULL CHECK (member_type IN ('hosted', 'proxy')),
    endpoint TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (group_name, position),
    UNIQUE (group_name, name)
);

CREATE TABLE IF NOT EXISTS resolver_audit_log (
    id BIGSERIAL PRIMARY KEY,
    group_name TEXT NOT NULL,
    repository TEXT NOT NULL,
    member_name TEXT,
    outcome TEXT NOT NULL,
    actor TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS resolver_audit_log_group_occurred_at_idx ON resolver_audit_log (group_name, occurred_at DESC);
