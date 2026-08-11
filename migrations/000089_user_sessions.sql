-- +goose Up
CREATE TABLE user_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('local_session', 'oidc')),
    ip_address TEXT NOT NULL DEFAULT '' CHECK (length(ip_address) <= 64),
    user_agent TEXT NOT NULL DEFAULT '' CHECK (length(user_agent) <= 512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at > created_at)
);

CREATE INDEX user_sessions_user_active_idx
    ON user_sessions (user_id, created_at DESC, id)
    WHERE revoked_at IS NULL;

CREATE INDEX user_sessions_expiry_idx
    ON user_sessions (expires_at, id);

-- +goose Down
DROP TABLE IF EXISTS user_sessions;
