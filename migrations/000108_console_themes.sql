-- +goose Up
ALTER TABLE site_settings
    ADD COLUMN enabled_theme_ids TEXT[] NOT NULL DEFAULT ARRAY['aerok-dark', 'aerok-light']::TEXT[],
    ADD COLUMN default_theme_id TEXT NOT NULL DEFAULT 'aerok-dark',
    ADD CONSTRAINT site_settings_enabled_themes_nonempty CHECK (cardinality(enabled_theme_ids) BETWEEN 1 AND 32),
    ADD CONSTRAINT site_settings_default_theme_nonempty CHECK (length(btrim(default_theme_id)) BETWEEN 2 AND 64);

-- +goose Down
ALTER TABLE site_settings
    DROP CONSTRAINT IF EXISTS site_settings_default_theme_nonempty,
    DROP CONSTRAINT IF EXISTS site_settings_enabled_themes_nonempty,
    DROP COLUMN IF EXISTS default_theme_id,
    DROP COLUMN IF EXISTS enabled_theme_ids;
