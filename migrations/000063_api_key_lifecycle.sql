-- +goose Up
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS api_keys_active_expiry_idx
    ON api_keys (expires_at)
    WHERE revoked_at IS NULL;

-- +goose Down
-- Forward-only: application rollback keeps API-key lifecycle metadata.
