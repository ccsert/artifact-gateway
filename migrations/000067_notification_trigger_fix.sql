-- +goose Up
-- All notification source tables expose an id column. Avoid referencing
-- table-specific fields from a generic trigger record: PL/pgSQL resolves
-- NEW.field at runtime and would fail on tables without that field.
CREATE OR REPLACE FUNCTION artifact_gateway_notify_queue() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(TG_ARGV[0], COALESCE(NEW.id::text, 'wake'));
    RETURN NEW;
END;
$$;

-- +goose Down
-- Keep the corrected trigger function in place on rollback; the previous
-- generic implementation was not safe for all trigger relations.
