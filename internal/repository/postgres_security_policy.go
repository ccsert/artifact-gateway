package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) GetRepositorySecurityPolicy(ctx context.Context, repositoryID string) (RepositorySecurityPolicy, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositorySecurityPolicy{}, err
	}
	if !exists {
		return RepositorySecurityPolicy{}, ErrNotFound
	}
	policy := DefaultRepositorySecurityPolicy()
	var licenses []byte
	err := s.db.QueryRowContext(ctx, `SELECT version::text,enabled,auto_scan_on_publish,require_signature,require_verified_signature,require_sbom,require_provenance,require_vulnerability_scan,max_allowed_severity,fail_on_scan_error,array_to_json(allowed_licenses) FROM repository_security_policies WHERE repository_id::text=$1`, repositoryID).Scan(&policy.Version, &policy.Enabled, &policy.AutoScanOnPublish, &policy.RequireSignature, &policy.RequireVerifiedSignature, &policy.RequireSBOM, &policy.RequireProvenance, &policy.RequireVulnerabilityScan, &policy.MaxAllowedSeverity, &policy.FailOnScanError, &licenses)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return RepositorySecurityPolicy{}, err
	}
	if err := json.Unmarshal(licenses, &policy.AllowedLicenses); err != nil {
		return RepositorySecurityPolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) ReplaceRepositorySecurityPolicy(ctx context.Context, repositoryID string, policy RepositorySecurityPolicy, expectedVersion string) (RepositorySecurityPolicy, error) {
	if policy.MaxAllowedSeverity == "" {
		policy.MaxAllowedSeverity = DefaultRepositorySecurityPolicy().MaxAllowedSeverity
	}
	if policy.AllowedLicenses == nil {
		policy.AllowedLicenses = []string{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositorySecurityPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositorySecurityPolicy{}, err
	}
	if !exists {
		return RepositorySecurityPolicy{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_security_policies (repository_id,version,enabled,auto_scan_on_publish,require_signature,require_verified_signature,require_sbom,require_provenance,require_vulnerability_scan,max_allowed_severity,fail_on_scan_error,allowed_licenses) VALUES ($1,1,false,false,false,false,false,false,false,'critical',true,'{}') ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositorySecurityPolicy{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE repository_security_policies SET version=version+1,enabled=$3,auto_scan_on_publish=$4,require_signature=$5,require_verified_signature=$6,require_sbom=$7,require_provenance=$8,require_vulnerability_scan=$9,max_allowed_severity=$10,fail_on_scan_error=$11,allowed_licenses=COALESCE($12::text[],'{}') WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion, policy.Enabled, policy.AutoScanOnPublish, policy.RequireSignature, policy.RequireVerifiedSignature, policy.RequireSBOM, policy.RequireProvenance, policy.RequireVulnerabilityScan, policy.MaxAllowedSeverity, policy.FailOnScanError, policy.AllowedLicenses).Scan(&policy.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositorySecurityPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return RepositorySecurityPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return RepositorySecurityPolicy{}, err
	}
	return CloneRepositorySecurityPolicy(policy), nil
}
