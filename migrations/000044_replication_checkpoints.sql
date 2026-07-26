-- +goose Up
CREATE TABLE replication_plans (
    id UUID PRIMARY KEY,
    source_repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    target_repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    format TEXT NOT NULL CHECK (format IN ('raw', 'oci', 'maven', 'conan')),
    idempotency_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed')) DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    UNIQUE (target_repository_id, idempotency_key)
);

CREATE TABLE replication_checkpoints (
    plan_id UUID NOT NULL REFERENCES replication_plans(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size >= 0),
    byte_offset BIGINT NOT NULL DEFAULT 0 CHECK (byte_offset >= 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'copying', 'verified', 'failed')) DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, object_key)
);
CREATE INDEX replication_plans_claim_idx ON replication_plans (created_at) WHERE state IN ('pending', 'failed');
CREATE INDEX replication_checkpoints_pending_idx ON replication_checkpoints (plan_id, state, updated_at);

-- +goose Down
DROP TABLE replication_checkpoints;
DROP TABLE replication_plans;
