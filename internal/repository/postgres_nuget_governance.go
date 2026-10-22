package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) TransitionNuGetGovernance(ctx context.Context, in NuGetGovernanceChange) (NuGetPublication, error) {
	if !validNuGetGovernanceChange(in) {
		return NuGetPublication{}, ErrInvalidNuGetPublication
	}
	before, err := s.GetNuGetPublication(ctx, in.RepositoryID, in.PackageID, in.Version)
	if err != nil {
		return NuGetPublication{}, err
	}
	if before.SourceVersion != "" && (in.Action == "unlist" || in.Action == "relist") {
		return NuGetPublication{}, ErrInvalidNuGetPublication
	}
	tx, name, err := s.nuGetTransaction(ctx, in.RepositoryID, before.SourceVersion)
	if err != nil {
		return NuGetPublication{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanNuGetPublication(tx.QueryRowContext(ctx, `SELECT `+nuGetPublicationColumns+` FROM native_nuget_publications WHERE session_id::text=$1 FOR UPDATE`, before.SessionID))
	if err != nil {
		return NuGetPublication{}, err
	}
	p, changed, err := transitionNuGetGovernance(p, in)
	if err != nil {
		return NuGetPublication{}, err
	}
	if changed {
		g := p.Governance
		if _, err = tx.ExecContext(ctx, `UPDATE native_nuget_publications SET governance_version=$2,listed=$3,tombstoned=$4,quarantined=$5,governance_updated_at=$6,governance_updated_by=$7,governance_reason=$8 WHERE session_id::text=$1`, p.SessionID, g.Version, g.Listed, g.Tombstoned, g.Quarantined, g.UpdatedAt, g.UpdatedBy, g.Reason); err != nil {
			return NuGetPublication{}, err
		}
		if err = insertAudit(ctx, tx, nuGetGovernanceAudit(p, in, name)); err != nil {
			return NuGetPublication{}, err
		}
	}
	return p, tx.Commit()
}

func (s *PostgresStore) ReplaceNuGetQuarantineReadPolicy(ctx context.Context, id string, policy RepositoryQuarantineReadPolicy, expectedVersion, actor string) (RepositoryQuarantineReadPolicy, error) {
	repo, err := s.GetHostedRepository(ctx, id)
	if err != nil || repo.Format != FormatNuGet {
		if err == nil {
			err = ErrNotFound
		}
		return RepositoryQuarantineReadPolicy{}, err
	}
	binding := ""
	if repo.Type == RepositoryTypeProxy {
		binding = repo.Version
	}
	tx, _, err := s.nuGetTransaction(ctx, id, binding)
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_quarantine_read_policies(repository_id,version,enabled) VALUES($1,1,false) ON CONFLICT DO NOTHING`, id); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE repository_quarantine_read_policies SET version=version+1,enabled=$3 WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, id, expectedVersion, policy.Enabled).Scan(&policy.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryQuarantineReadPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if err = insertAudit(ctx, tx, nuGetReadPolicyAudit(repo, policy, actor)); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	return policy, tx.Commit()
}
