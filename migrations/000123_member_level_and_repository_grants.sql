-- +goose Up
-- A member account carries no repository capability of its own, so the legacy
-- global reader and writer roles are converted into explicit per-repository
-- grants covering every repository that exists now. A repository created later
-- is deliberately not covered: gaining access to a new repository stays an
-- explicit action rather than a side effect of an old role.
--
-- An explicit per-principal grant is evaluated regardless of the grant-set
-- version, so materializing grants here does not switch a repository away from
-- its legacy static readers and writers.
INSERT INTO repository_grant_sets (repository_id, version)
SELECT id, 1 FROM hosted_repositories WHERE state = 'active'
ON CONFLICT (repository_id) DO NOTHING;

INSERT INTO repository_grants (repository_id, principal, scopes, resource_prefix)
SELECT r.id, 'user:' || u.name, ARRAY['repositories:read'], ''
FROM hosted_repositories r
CROSS JOIN users u
WHERE r.state = 'active' AND u.role = 'reader'
ON CONFLICT DO NOTHING;

INSERT INTO repository_grants (repository_id, principal, scopes, resource_prefix)
SELECT r.id, 'user:' || u.name, ARRAY['repositories:write'], ''
FROM hosted_repositories r
CROSS JOIN users u
WHERE r.state = 'active' AND u.role = 'writer'
ON CONFLICT DO NOTHING;

UPDATE users
SET role = 'member', updated_at = now(), version = version + 1
WHERE role IN ('reader', 'writer');

ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('none', 'member', 'reader', 'writer', 'admin'));

-- +goose Down
-- The role conversion is forward-only: restoring reader or writer from member
-- would mean deciding which of the materialized grants to discard. The
-- constraint change alone is reversible, as long as no setting uses member.
ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('none', 'reader', 'writer', 'admin'));
