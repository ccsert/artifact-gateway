-- +goose Up
CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 128),
    endpoint_url TEXT NOT NULL CHECK (length(endpoint_url) BETWEEN 1 AND 2048),
    secret_ciphertext TEXT NOT NULL CHECK (secret_ciphertext <> ''),
    event_types JSONB NOT NULL CHECK (
        jsonb_typeof(event_types) = 'array'
        AND jsonb_array_length(event_types) > 0
        AND event_types <@ '["artifact.quarantined", "artifact.released"]'::jsonb
    ),
    enabled BOOLEAN NOT NULL DEFAULT true,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY,
    event_type TEXT NOT NULL CHECK (event_type IN ('artifact.quarantined', 'artifact.released')),
    occurred_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id UUID PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
    subscription_id UUID NOT NULL REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivering', 'retrying', 'succeeded', 'dead')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 8),
    next_attempt_at TIMESTAMPTZ,
    lease_owner TEXT,
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_status INTEGER CHECK (last_status IS NULL OR last_status BETWEEN 100 AND 599),
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 1024),
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id, subscription_id),
    CHECK ((state = 'delivering') = (lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((state = 'succeeded') = (delivered_at IS NOT NULL)),
    CHECK ((state IN ('pending', 'retrying')) = (next_attempt_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS webhook_deliveries_actionable_idx
    ON webhook_deliveries (COALESCE(next_attempt_at, lease_expires_at), created_at)
    WHERE state IN ('pending', 'retrying', 'delivering');
CREATE INDEX IF NOT EXISTS webhook_deliveries_subscription_idx
    ON webhook_deliveries (subscription_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS webhook_subscriptions;
