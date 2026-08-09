-- +goose Up
CREATE TABLE IF NOT EXISTS authorization_templates (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    grants JSONB NOT NULL DEFAULT '[]'::jsonb,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS authorization_templates_updated_at_idx
    ON authorization_templates (updated_at DESC, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS authorization_templates_name_ci_idx
    ON authorization_templates (lower(name));

-- +goose Down
DROP TABLE IF EXISTS authorization_templates;
