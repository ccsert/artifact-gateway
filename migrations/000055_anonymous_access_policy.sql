-- +goose Up
CREATE TABLE IF NOT EXISTS anonymous_access_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    version BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT false
);
INSERT INTO anonymous_access_policy (singleton) VALUES (true) ON CONFLICT DO NOTHING;

-- +goose Down
-- Global access policy migrations are forward-only; compensate with a later migration.
