-- +goose Up
CREATE OR REPLACE FUNCTION artifact_gateway_notify_queue() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(TG_ARGV[0], COALESCE(NEW.id::text, NEW.repository_id::text, 'wake'));
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS lifecycle_jobs_notify ON lifecycle_jobs;
CREATE TRIGGER lifecycle_jobs_notify
AFTER INSERT OR UPDATE OF state, next_attempt_at ON lifecycle_jobs
FOR EACH ROW WHEN (NEW.state IN ('pending', 'retrying'))
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_lifecycle_jobs');

DROP TRIGGER IF EXISTS replication_plans_notify ON replication_plans;
CREATE TRIGGER replication_plans_notify
AFTER INSERT OR UPDATE OF state, next_attempt_at ON replication_plans
FOR EACH ROW WHEN (NEW.state IN ('pending', 'failed'))
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_replication_plans');

DROP TRIGGER IF EXISTS audit_cleanup_jobs_notify ON audit_cleanup_jobs;
CREATE TRIGGER audit_cleanup_jobs_notify
AFTER INSERT OR UPDATE OF state ON audit_cleanup_jobs
FOR EACH ROW WHEN (NEW.state IN ('pending', 'failed'))
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_audit_cleanup');

DROP TRIGGER IF EXISTS hosted_repositories_deletion_notify ON hosted_repositories;
CREATE TRIGGER hosted_repositories_deletion_notify
AFTER UPDATE OF state ON hosted_repositories
FOR EACH ROW WHEN (NEW.state = 'deleting')
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_repository_deletions');

-- +goose Down
DROP TRIGGER IF EXISTS hosted_repositories_deletion_notify ON hosted_repositories;
DROP TRIGGER IF EXISTS audit_cleanup_jobs_notify ON audit_cleanup_jobs;
DROP TRIGGER IF EXISTS replication_plans_notify ON replication_plans;
DROP TRIGGER IF EXISTS lifecycle_jobs_notify ON lifecycle_jobs;
DROP FUNCTION IF EXISTS artifact_gateway_notify_queue();
