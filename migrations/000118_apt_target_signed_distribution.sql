-- +goose Up
ALTER TABLE replication_plans DROP CONSTRAINT replication_plans_format_check;
ALTER TABLE replication_plans ADD CONSTRAINT replication_plans_format_check CHECK (format IN ('maven','oci','raw','conan','npm','pypi','go','apt'));
-- Persist the requested target suite across retries; pool coordinates stay
-- repository-global and retain the existing scan/quarantine identity.
ALTER TABLE replication_plans ADD COLUMN apt_target_suite text NOT NULL DEFAULT '';
ALTER TABLE replication_plans ADD CONSTRAINT replication_apt_target_suite_format
 CHECK (apt_target_suite = '' OR (format = 'apt' AND apt_target_suite ~ '^[a-z0-9][a-z0-9.+-]*$' AND length(apt_target_suite) <= 128));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'APT distribution requires backup restoration for downgrade'; END $$;
-- +goose StatementEnd
