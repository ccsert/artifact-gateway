-- +goose Up
CREATE TABLE IF NOT EXISTS repository_capacity_quotas (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    quota_bytes BIGINT NOT NULL DEFAULT 0 CHECK (quota_bytes >= 0)
);

-- +goose Down
DROP TABLE repository_capacity_quotas;
