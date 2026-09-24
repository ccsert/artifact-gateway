-- +goose Up
ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('none', 'reader', 'writer', 'admin'));

-- +goose Down
-- PostgreSQL rejects this downgrade while any saved settings use 'none'.
ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('reader', 'writer', 'admin'));
