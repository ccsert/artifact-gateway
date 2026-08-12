package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) GetRepositoryQuarantineReadPolicy(ctx context.Context, repositoryID string) (RepositoryQuarantineReadPolicy, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if !exists {
		return RepositoryQuarantineReadPolicy{}, ErrNotFound
	}
	policy := DefaultRepositoryQuarantineReadPolicy()
	err := s.db.QueryRowContext(ctx, `SELECT version::text,enabled FROM repository_quarantine_read_policies WHERE repository_id::text=$1`, repositoryID).Scan(&policy.Version, &policy.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	return policy, err
}

func (s *PostgresStore) ReplaceRepositoryQuarantineReadPolicy(ctx context.Context, repositoryID string, policy RepositoryQuarantineReadPolicy, expectedVersion string) (RepositoryQuarantineReadPolicy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if !exists {
		return RepositoryQuarantineReadPolicy{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_quarantine_read_policies (repository_id,version,enabled) VALUES ($1,1,false) ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE repository_quarantine_read_policies SET version=version+1,enabled=$3 WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion, policy.Enabled).Scan(&policy.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryQuarantineReadPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	return policy, nil
}
