-- +goose Up
CREATE TABLE IF NOT EXISTS site_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    site_name TEXT NOT NULL DEFAULT 'Artifact Gateway',
    logo_url TEXT NOT NULL DEFAULT '',
    brand_mark TEXT NOT NULL DEFAULT 'AG',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(btrim(site_name)) BETWEEN 1 AND 80),
    CHECK (length(logo_url) <= 262144),
    CHECK (length(btrim(brand_mark)) BETWEEN 1 AND 8)
);

INSERT INTO site_settings (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS site_settings;
