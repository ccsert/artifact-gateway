-- +goose Up
ALTER TABLE users
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN email TEXT NOT NULL DEFAULT '',
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_login_at TIMESTAMPTZ,
    ADD COLUMN password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN failed_login_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_login_attempts >= 0),
    ADD COLUMN locked_until TIMESTAMPTZ,
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN session_version BIGINT NOT NULL DEFAULT 1 CHECK (session_version > 0);

CREATE UNIQUE INDEX users_name_case_insensitive_idx ON users (lower(name));
CREATE INDEX users_management_filter_idx ON users (state, role, created_at, id);

-- +goose Down
DROP INDEX IF EXISTS users_management_filter_idx;
DROP INDEX IF EXISTS users_name_case_insensitive_idx;
ALTER TABLE users
    DROP COLUMN IF EXISTS session_version,
    DROP COLUMN IF EXISTS must_change_password,
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS failed_login_attempts,
    DROP COLUMN IF EXISTS password_changed_at,
    DROP COLUMN IF EXISTS last_login_at,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS display_name;
