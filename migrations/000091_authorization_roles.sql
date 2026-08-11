-- +goose Up
CREATE TABLE IF NOT EXISTS authorization_roles (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    scopes TEXT[] NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT authorization_roles_scopes_nonempty CHECK (cardinality(scopes) > 0)
);

CREATE INDEX IF NOT EXISTS authorization_roles_updated_at_idx
    ON authorization_roles (updated_at DESC, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS authorization_roles_name_ci_idx
    ON authorization_roles (lower(name));

-- +goose Down
DROP TABLE IF EXISTS authorization_roles;
