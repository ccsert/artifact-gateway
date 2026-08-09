-- +goose Up
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    kind TEXT NOT NULL CHECK (kind IN ('repository-retention', 'audit-retention')),
    repository_id UUID REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds BETWEEN 900 AND 31536000),
    enabled BOOLEAN NOT NULL DEFAULT true,
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    last_run_id UUID,
    last_run_state TEXT NOT NULL DEFAULT 'failed' CHECK (last_run_state IN ('submitted', 'failed')),
    last_error TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'audit-retention' AND repository_id IS NULL) OR (kind = 'repository-retention' AND repository_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS scheduled_tasks_name_idx ON scheduled_tasks (lower(name));
CREATE INDEX IF NOT EXISTS scheduled_tasks_due_idx ON scheduled_tasks (next_run_at) WHERE enabled;

CREATE TABLE IF NOT EXISTS scheduled_task_runs (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual')),
    state TEXT NOT NULL CHECK (state IN ('submitted', 'failed')),
    scheduled_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    target_kind TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS scheduled_task_runs_task_idx ON scheduled_task_runs (task_id, created_at DESC);
