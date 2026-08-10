-- +goose Up
ALTER TABLE users
    ALTER COLUMN password_changed_at DROP NOT NULL,
    ALTER COLUMN password_changed_at DROP DEFAULT;

ALTER TABLE oidc_settings
    ADD COLUMN provisioning_mode TEXT NOT NULL DEFAULT 'disabled'
        CHECK (provisioning_mode IN ('disabled', 'jit')),
    ADD COLUMN email_linking_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN jit_default_role TEXT NOT NULL DEFAULT 'reader'
        CHECK (jit_default_role IN ('admin', 'writer', 'reader'));

CREATE TABLE user_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind = 'oidc'),
    issuer TEXT NOT NULL CHECK (issuer <> ''),
    subject TEXT NOT NULL CHECK (subject <> ''),
    email TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, issuer, subject),
    UNIQUE (user_id, kind, issuer)
);

CREATE INDEX user_identities_user_idx
    ON user_identities (user_id, created_at, id);

-- +goose Down
DROP TABLE IF EXISTS user_identities;

ALTER TABLE oidc_settings
    DROP COLUMN IF EXISTS jit_default_role,
    DROP COLUMN IF EXISTS email_linking_enabled,
    DROP COLUMN IF EXISTS provisioning_mode;

UPDATE users SET password_changed_at = now() WHERE password_changed_at IS NULL;
ALTER TABLE users
    ALTER COLUMN password_changed_at SET DEFAULT now(),
    ALTER COLUMN password_changed_at SET NOT NULL;
