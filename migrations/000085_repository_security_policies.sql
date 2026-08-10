-- +goose Up
CREATE TABLE IF NOT EXISTS repository_security_policies (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    enabled BOOLEAN NOT NULL DEFAULT false,
    require_signature BOOLEAN NOT NULL DEFAULT false,
    require_verified_signature BOOLEAN NOT NULL DEFAULT false,
    require_sbom BOOLEAN NOT NULL DEFAULT false,
    require_provenance BOOLEAN NOT NULL DEFAULT false,
    require_vulnerability_scan BOOLEAN NOT NULL DEFAULT false,
    max_allowed_severity TEXT NOT NULL DEFAULT 'critical' CHECK (max_allowed_severity IN ('none', 'low', 'medium', 'high', 'critical')),
    fail_on_scan_error BOOLEAN NOT NULL DEFAULT true,
    allowed_licenses TEXT[] NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE IF EXISTS repository_security_policies;
