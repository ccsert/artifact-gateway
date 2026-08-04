-- +goose Up
ALTER TABLE lifecycle_jobs DROP CONSTRAINT IF EXISTS lifecycle_jobs_state_check;
ALTER TABLE lifecycle_jobs ADD CONSTRAINT lifecycle_jobs_state_check CHECK (state IN ('pending', 'running', 'retrying', 'completed', 'failed', 'cancelled'));
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0);
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 20);
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS progress_current INTEGER NOT NULL DEFAULT 0 CHECK (progress_current >= 0);
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS progress_total INTEGER NOT NULL DEFAULT 0 CHECK (progress_total >= 0);
ALTER TABLE lifecycle_jobs ADD COLUMN IF NOT EXISTS progress_message TEXT NOT NULL DEFAULT '';
UPDATE lifecycle_jobs SET next_attempt_at=created_at WHERE state='pending' AND next_attempt_at IS NULL;
UPDATE lifecycle_jobs SET lease_expires_at=now() WHERE state='running' AND lease_expires_at IS NULL;
CREATE INDEX IF NOT EXISTS lifecycle_jobs_actionable_idx ON lifecycle_jobs (next_attempt_at, created_at) WHERE state IN ('pending', 'retrying');
CREATE INDEX IF NOT EXISTS lifecycle_jobs_lease_idx ON lifecycle_jobs (lease_expires_at) WHERE state='running';
