-- +goose Up
ALTER TABLE site_settings
    ALTER COLUMN enabled_theme_ids SET DEFAULT ARRAY['gateway-dark', 'gateway-light', 'aerok-dark', 'aerok-light']::TEXT[],
    ALTER COLUMN default_theme_id SET DEFAULT 'gateway-dark';

UPDATE site_settings
SET enabled_theme_ids = ARRAY['gateway-dark', 'gateway-light', 'aerok-dark', 'aerok-light']::TEXT[],
    default_theme_id = 'gateway-dark'
WHERE enabled_theme_ids = ARRAY['aerok-dark', 'aerok-light']::TEXT[]
  AND default_theme_id = 'aerok-dark';

-- +goose Down
UPDATE site_settings
SET enabled_theme_ids = ARRAY['aerok-dark', 'aerok-light']::TEXT[],
    default_theme_id = 'aerok-dark'
WHERE enabled_theme_ids = ARRAY['gateway-dark', 'gateway-light', 'aerok-dark', 'aerok-light']::TEXT[]
  AND default_theme_id = 'gateway-dark';

ALTER TABLE site_settings
    ALTER COLUMN enabled_theme_ids SET DEFAULT ARRAY['aerok-dark', 'aerok-light']::TEXT[],
    ALTER COLUMN default_theme_id SET DEFAULT 'aerok-dark';
