-- +goose Up
CREATE TABLE console_theme_packages (
    id TEXT PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT console_theme_packages_id_valid CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,63}$'),
    CONSTRAINT console_theme_packages_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

-- +goose Down
DROP TABLE IF EXISTS console_theme_packages;
