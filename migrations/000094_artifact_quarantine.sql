-- +goose Up
CREATE TABLE IF NOT EXISTS artifact_quarantines (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    format TEXT NOT NULL CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt')),
    coordinate TEXT NOT NULL CHECK (char_length(coordinate) BETWEEN 1 AND 1024 AND btrim(coordinate) <> ''),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[a-f0-9]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('quarantined', 'released')),
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1024 AND btrim(reason) <> ''),
    updated_by TEXT NOT NULL CHECK (char_length(updated_by) BETWEEN 1 AND 512 AND btrim(updated_by) <> ''),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    quarantined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state = 'quarantined' AND released_at IS NULL) OR (state = 'released' AND released_at IS NOT NULL)),
    CHECK (format <> 'conan' OR coordinate ~ '^[^/#]+/[^/#]+/[^/#]+/[^/#]+#[^/#]+$'),
    PRIMARY KEY (repository_id, format, coordinate, digest)
);

-- +goose Down
DROP TABLE IF EXISTS artifact_quarantines;
