-- +goose Up
CREATE TABLE IF NOT EXISTS artifact_intelligence (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    format TEXT NOT NULL,
    coordinate TEXT NOT NULL CHECK (char_length(coordinate) BETWEEN 1 AND 1024),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[a-f0-9]{64}$'),
    signatures JSONB NOT NULL DEFAULT '[]'::jsonb,
    sboms JSONB NOT NULL DEFAULT '[]'::jsonb,
    provenance JSONB,
    licenses JSONB NOT NULL DEFAULT '[]'::jsonb,
    vulnerability JSONB,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository_id, format, coordinate, digest)
);
CREATE INDEX IF NOT EXISTS artifact_intelligence_updated_idx ON artifact_intelligence (updated_at DESC);

-- +goose Down
DROP TABLE IF EXISTS artifact_intelligence;
