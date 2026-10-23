-- Authentication kind is server-owned evidence, never inferred from actor names.
-- Empty kinds leave existing formats unchanged; PyPI refuses unbound authority.
ALTER TABLE nexus_migration_batches ADD COLUMN actor_kind TEXT NOT NULL DEFAULT ''
    CHECK (actor_kind IN ('','static_admin','static_resolver','local_session','oidc','api_key','service_account_credential'));
ALTER TABLE nexus_migration_schedules ADD COLUMN actor_kind TEXT NOT NULL DEFAULT ''
    CHECK (actor_kind IN ('','static_admin','static_resolver','local_session','oidc','api_key','service_account_credential'));

ALTER TABLE nexus_migration_batches ADD COLUMN owner_id UUID;
ALTER TABLE nexus_migration_schedules ADD COLUMN owner_id UUID;
