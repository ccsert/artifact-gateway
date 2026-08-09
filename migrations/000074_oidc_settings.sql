-- +goose Up
CREATE TABLE IF NOT EXISTS oidc_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    issuer TEXT NOT NULL DEFAULT '',
    audience TEXT NOT NULL DEFAULT '',
    jwks_url TEXT NOT NULL DEFAULT '',
    client_id TEXT NOT NULL DEFAULT '',
    client_secret TEXT NOT NULL DEFAULT '',
    redirect_url TEXT NOT NULL DEFAULT '',
    scopes JSONB NOT NULL DEFAULT '["openid", "profile", "email"]'::JSONB CHECK (jsonb_typeof(scopes) = 'array'),
    admin_subjects JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(admin_subjects) = 'array'),
    reader_roles JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(reader_roles) = 'array'),
    writer_roles JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(writer_roles) = 'array'),
    admin_roles JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(admin_roles) = 'array'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS oidc_settings;
