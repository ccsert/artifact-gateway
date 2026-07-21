-- +goose Up
CREATE TABLE IF NOT EXISTS conan_groups (
    name TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS conan_group_members (
    group_name TEXT NOT NULL REFERENCES conan_groups(name) ON DELETE CASCADE,
    name TEXT NOT NULL,
    member_type TEXT NOT NULL CHECK (member_type IN ('hosted', 'proxy')),
    endpoint TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (group_name, position),
    UNIQUE (group_name, name)
);
