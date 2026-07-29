-- +goose Up
-- Proxy becomes a first-class Repository kind. hosted_repositories gains a
-- repo_type discriminator plus the proxy-only upstream endpoint and allowed
-- host list. All statements are idempotent because the migrate container
-- replays every migration on each start.
ALTER TABLE hosted_repositories ADD COLUMN IF NOT EXISTS repo_type TEXT NOT NULL DEFAULT 'hosted';
ALTER TABLE hosted_repositories ADD COLUMN IF NOT EXISTS endpoint TEXT NOT NULL DEFAULT '';
ALTER TABLE hosted_repositories ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'hosted_repositories_repo_type_check') THEN
        ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_repo_type_check CHECK (repo_type IN ('hosted', 'proxy'));
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'hosted_repositories_repo_shape_check') THEN
        ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_repo_shape_check CHECK (
            (repo_type = 'hosted' AND endpoint = '') OR
            (repo_type = 'proxy' AND endpoint <> '')
        );
    END IF;
END $$;
