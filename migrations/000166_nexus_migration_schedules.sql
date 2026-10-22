-- Migration schedules create ordinary durable batches; they never own content.
CREATE TABLE nexus_migration_schedules (
    id uuid PRIMARY KEY,
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    request jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('active','waiting_capacity','paused','blocked','stopped')),
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    interval_seconds integer NOT NULL CHECK (interval_seconds BETWEEN 60 AND 2592000),
    next_run_at timestamptz,
    last_successful_at timestamptz,
    consumed_observed_at timestamptz,
    capacity_evidence jsonb,
    active_batch_id uuid REFERENCES nexus_migration_batches(id),
    last_batch_id uuid REFERENCES nexus_migration_batches(id),
    last_error text NOT NULL DEFAULT '' CHECK(length(last_error)<=128),
    dispatch_attempts integer NOT NULL DEFAULT 0 CHECK(dispatch_attempts BETWEEN 0 AND 3),
    lease_token text NOT NULL DEFAULT '',
    lease_epoch bigint NOT NULL DEFAULT 0,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor,idempotency_key),
    CHECK ((state='stopped')=(next_run_at IS NULL))
);
CREATE INDEX nexus_migration_schedules_due ON nexus_migration_schedules(next_run_at) WHERE state='active';
ALTER TABLE nexus_migration_batches ADD COLUMN schedule_id uuid REFERENCES nexus_migration_schedules(id);
ALTER TABLE nexus_migration_batches ADD COLUMN schedule_epoch bigint;
CREATE UNIQUE INDEX nexus_migration_schedule_occurrence ON nexus_migration_batches(schedule_id,schedule_epoch) WHERE schedule_id IS NOT NULL;
CREATE TABLE nexus_migration_schedule_controls (
    schedule_id uuid NOT NULL REFERENCES nexus_migration_schedules(id),
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    operation text NOT NULL,
    request_hash text NOT NULL,
    response jsonb NOT NULL,
    PRIMARY KEY(schedule_id,actor,idempotency_key)
);
