-- +goose Up
CREATE TABLE IF NOT EXISTS native_oci_manifest_tombstones (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    name TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    subject_digest TEXT,
    artifact_type TEXT NOT NULL DEFAULT '',
    tags TEXT[] NOT NULL DEFAULT '{}'::text[],
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, name, digest)
);

-- Older OCI tombstones retained enough object-intent data to remain
-- recoverable, but did not preserve media type or tags.
INSERT INTO native_oci_manifest_tombstones (
    repository_id,
    name,
    digest,
    object_key,
    media_type,
    size
)
SELECT
    t.repository_id,
    left(t.coordinate, length(t.coordinate) - length(t.digest) - 1),
    t.digest,
    i.object_key,
    'application/vnd.oci.image.manifest.v1+json',
    i.size
FROM artifact_tombstones t
JOIN native_oci_object_intents i
  ON i.repository_id = t.repository_id
 AND i.digest = t.digest
 AND i.collected_at IS NULL
WHERE t.format = 'oci'
  AND t.coordinate = left(t.coordinate, length(t.coordinate) - length(t.digest) - 1) || '@' || t.digest
  AND NOT EXISTS (
      SELECT 1
      FROM native_oci_object_intents duplicate
      WHERE duplicate.repository_id = i.repository_id
        AND duplicate.digest = i.digest
        AND duplicate.collected_at IS NULL
        AND duplicate.object_key <> i.object_key
  )
ON CONFLICT DO NOTHING;
