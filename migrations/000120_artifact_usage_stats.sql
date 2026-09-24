-- +goose Up
CREATE TABLE IF NOT EXISTS artifact_usage_stats (
    repository TEXT NOT NULL,
    format TEXT NOT NULL,
    resource TEXT NOT NULL,
    download_count BIGINT NOT NULL DEFAULT 0,
    total_bytes BIGINT NOT NULL DEFAULT 0,
    first_downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_actor TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository, format, resource)
);
CREATE INDEX IF NOT EXISTS artifact_usage_stats_repository_usage_idx
    ON artifact_usage_stats (repository, download_count DESC);
