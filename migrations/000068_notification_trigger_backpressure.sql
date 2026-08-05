-- +goose Up
-- NOTIFY is a wake-up hint, not a retry loop. In particular, a worker that
-- transitions a job to failed must not immediately wake itself forever.
DROP TRIGGER IF EXISTS lifecycle_jobs_notify ON lifecycle_jobs;
DROP TRIGGER IF EXISTS lifecycle_jobs_notify_insert ON lifecycle_jobs;
DROP TRIGGER IF EXISTS lifecycle_jobs_notify_update ON lifecycle_jobs;
CREATE TRIGGER lifecycle_jobs_notify_insert
AFTER INSERT ON lifecycle_jobs
FOR EACH ROW WHEN (NEW.state IN ('pending', 'retrying'))
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_lifecycle_jobs');
CREATE TRIGGER lifecycle_jobs_notify_update
AFTER UPDATE OF state, next_attempt_at ON lifecycle_jobs
FOR EACH ROW WHEN (NEW.state IN ('pending', 'retrying') AND OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_lifecycle_jobs');

DROP TRIGGER IF EXISTS replication_plans_notify ON replication_plans;
DROP TRIGGER IF EXISTS replication_plans_notify_insert ON replication_plans;
DROP TRIGGER IF EXISTS replication_plans_notify_update ON replication_plans;
CREATE TRIGGER replication_plans_notify_insert
AFTER INSERT ON replication_plans
FOR EACH ROW WHEN (NEW.state IN ('pending', 'failed'))
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_replication_plans');
CREATE TRIGGER replication_plans_notify_update
AFTER UPDATE OF state, next_attempt_at ON replication_plans
FOR EACH ROW WHEN (NEW.state = 'pending' AND OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_replication_plans');

DROP TRIGGER IF EXISTS audit_cleanup_jobs_notify ON audit_cleanup_jobs;
DROP TRIGGER IF EXISTS audit_cleanup_jobs_notify_insert ON audit_cleanup_jobs;
DROP TRIGGER IF EXISTS audit_cleanup_jobs_notify_update ON audit_cleanup_jobs;
CREATE TRIGGER audit_cleanup_jobs_notify_insert
AFTER INSERT ON audit_cleanup_jobs
FOR EACH ROW WHEN (NEW.state = 'pending')
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_audit_cleanup');
CREATE TRIGGER audit_cleanup_jobs_notify_update
AFTER UPDATE OF state ON audit_cleanup_jobs
FOR EACH ROW WHEN (NEW.state = 'pending' AND OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION artifact_gateway_notify_queue('artifact_gateway_audit_cleanup');

-- +goose Down
-- Keep the backpressure-safe trigger definitions on rollback.
