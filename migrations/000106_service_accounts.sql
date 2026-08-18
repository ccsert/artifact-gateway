-- +goose Up
CREATE TABLE IF NOT EXISTS service_accounts (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version BIGINT NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX IF NOT EXISTS service_accounts_name_unique_idx
    ON service_accounts (lower(name));

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS service_account_id UUID REFERENCES service_accounts(id);

CREATE INDEX IF NOT EXISTS api_keys_service_account_idx
    ON api_keys (service_account_id, id)
    WHERE service_account_id IS NOT NULL;

-- +goose Down
-- Forward-only: service principals and credential audit history are retained.
