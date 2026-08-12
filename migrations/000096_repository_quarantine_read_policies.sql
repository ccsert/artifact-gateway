-- +goose Up
CREATE TABLE IF NOT EXISTS repository_quarantine_read_policies (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    enabled BOOLEAN NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE IF EXISTS repository_quarantine_read_policies;
