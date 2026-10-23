-- +goose Up
-- Governance is independent from upload expiry: retained gems remain committed,
-- reserve their immutable identity and capacity, and are never collected.
ALTER TABLE native_rubygems_sessions
 ADD COLUMN governance_version BIGINT NOT NULL DEFAULT 1 CHECK(governance_version>=1),
 ADD COLUMN yanked BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN governance_updated_at TIMESTAMPTZ,
 ADD COLUMN governance_updated_by TEXT NOT NULL DEFAULT '',
 ADD COLUMN governance_reason TEXT NOT NULL DEFAULT '',
 ADD CONSTRAINT native_rubygems_governance_state CHECK(
  (governance_version=1 AND NOT yanked AND governance_updated_at IS NULL
   AND governance_updated_by='' AND governance_reason='') OR
  (governance_version>1 AND state='committed' AND governance_updated_at IS NOT NULL
   AND length(governance_updated_by) BETWEEN 1 AND 256
   AND length(governance_reason) BETWEEN 1 AND 2048));

-- +goose Down
-- Forward-only: preserve committed identities and their governance history.
