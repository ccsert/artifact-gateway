-- +goose Up
-- The reader and writer account levels are gone: no account, API key, OIDC
-- mapping, or JIT default can name them any more, and the database stops
-- accepting them too, so a level check cannot pass for a value the code no
-- longer recognizes.
--
-- The two OIDC role-mapping lists that existed to distinguish read from write
-- authority collapse into one member list, because a mapped external role now
-- approves an account instead of granting repository capability. An external
-- role listed in either legacy list stays mapped, to the member level.
UPDATE oidc_settings
SET reader_roles = (
    SELECT COALESCE(jsonb_agg(DISTINCT role), '[]'::jsonb)
    FROM jsonb_array_elements_text(reader_roles || writer_roles) AS role
);

ALTER TABLE oidc_settings
    RENAME COLUMN reader_roles TO member_roles;

ALTER TABLE oidc_settings
    DROP COLUMN writer_roles;

UPDATE oidc_settings
SET jit_default_role = 'member'
WHERE jit_default_role IN ('reader', 'writer');

ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('none', 'member', 'admin'));

-- A row that still names a removed level is narrowed before the constraint goes
-- on, keeping whatever authority the code still recognizes and dropping the
-- rest. That is what the row already resolved to: member carries no global
-- capability, and an authorization decision never granted anything for a value
-- the code stopped recognizing. There is no window in which this affects an
-- account created after the migration.
UPDATE users
SET role = 'member', updated_at = now(), version = version + 1
WHERE role NOT IN ('none', 'member', 'admin');

UPDATE api_keys
SET roles = COALESCE(
    (SELECT array_agg(DISTINCT kept) FROM unnest(roles) AS kept WHERE kept IN ('member', 'admin')),
    ARRAY['member'])
WHERE NOT (roles <@ ARRAY['member', 'admin']);

ALTER TABLE users
    ADD CONSTRAINT users_role_check CHECK (role IN ('none', 'member', 'admin'));

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_roles_check CHECK (roles <@ ARRAY['member', 'admin']);

-- +goose Down
-- The merged member list cannot be split back into reader and writer entries,
-- so the downgrade restores both legacy lists empty and widens the default-role
-- check again. A saved member default is rejected by the restored constraint
-- until it is changed, which keeps the downgrade from inventing a role.
ALTER TABLE api_keys
    DROP CONSTRAINT api_keys_roles_check;

ALTER TABLE users
    DROP CONSTRAINT users_role_check;

ALTER TABLE oidc_settings
    DROP CONSTRAINT oidc_settings_jit_default_role_check,
    ADD CONSTRAINT oidc_settings_jit_default_role_check
        CHECK (jit_default_role IN ('none', 'reader', 'writer', 'admin'));

ALTER TABLE oidc_settings
    ADD COLUMN writer_roles JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(writer_roles) = 'array');

ALTER TABLE oidc_settings
    RENAME COLUMN member_roles TO reader_roles;
