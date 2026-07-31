-- +goose Up
CREATE INDEX IF NOT EXISTS audit_cleanup_jobs_running_started_idx ON audit_cleanup_jobs (started_at) WHERE state='running';
