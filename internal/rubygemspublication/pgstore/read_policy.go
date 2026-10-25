package pgstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func (s *Store) GetReadPolicy(ctx context.Context, id string) (repository.RepositoryQuarantineReadPolicy, error) {
	var p repository.RepositoryQuarantineReadPolicy
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(p.version,1)::text,COALESCE(p.enabled,false) FROM hosted_repositories r LEFT JOIN repository_quarantine_read_policies p ON p.repository_id=r.id WHERE r.id::text=$1 AND r.format='rubygems' AND r.repo_type='hosted' AND r.state='active'`, id).Scan(&p.Version, &p.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = repository.ErrNotFound
	}
	return p, err
}

func (s *Store) ReplaceReadPolicy(ctx context.Context, c domain.PolicyChange, check domain.GovernanceCheck) (repository.RepositoryQuarantineReadPolicy, error) {
	if !domain.ValidPolicyChange(c) {
		return repository.RepositoryQuarantineReadPolicy{}, domain.ErrInvalid
	}
	tx, _, err := s.transaction(ctx, c.RepositoryID)
	if err != nil {
		return repository.RepositoryQuarantineReadPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_quarantine_read_policies(repository_id,version,enabled) VALUES($1,1,false) ON CONFLICT DO NOTHING`, c.RepositoryID); err != nil {
		return repository.RepositoryQuarantineReadPolicy{}, err
	}
	var current repository.RepositoryQuarantineReadPolicy
	if err = tx.QueryRowContext(ctx, `SELECT version::text,enabled FROM repository_quarantine_read_policies WHERE repository_id::text=$1 FOR UPDATE`, c.RepositoryID).Scan(&current.Version, &current.Enabled); err != nil {
		return current, err
	}
	a, err := governanceAuthorityTx(ctx, tx, domain.GovernanceChange{RepositoryID: c.RepositoryID, Identity: c.Identity, Actor: c.Actor})
	if err != nil {
		return current, err
	}
	next, audit, err := domain.ApplyReadPolicy(ctx, current, a, c, check)
	if err != nil {
		return current, err
	}
	if audit != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE repository_quarantine_read_policies SET version=$2,enabled=$3 WHERE repository_id::text=$1`, c.RepositoryID, next.Version, next.Enabled); err != nil {
			return current, err
		}
		if err = insertAudit(ctx, tx, *audit); err != nil {
			return current, err
		}
	}
	if err = domain.CheckGovernance(ctx, check, a); err != nil {
		return current, err
	}
	return next, tx.Commit()
}
