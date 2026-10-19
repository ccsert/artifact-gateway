package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) SetPyPIYank(ctx context.Context, mutation PyPIYankMutation) ([]PyPIFile, error) {
	release, err := lockArtifactDistributionCoordinates(ctx, s, mutation.RepositoryID, FormatPyPI, []string{mutation.Project + "@" + mutation.Version})
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var hosted bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM hosted_repositories WHERE id::text=$1 AND format='pypi' AND repo_type='hosted' AND state='active' FOR SHARE`, mutation.RepositoryID).Scan(&hosted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files WHERE repository_id::text=$1 AND project=$2 AND version=$3 AND state='visible' ORDER BY filename FOR UPDATE`, mutation.RepositoryID, mutation.Project, mutation.Version)
	if err != nil {
		return nil, err
	}
	var files []PyPIFile
	for rows.Next() {
		var file PyPIFile
		if err = scanPyPIFile(rows, &file); err != nil {
			_ = rows.Close()
			return nil, err
		}
		files = append(files, file)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	result, changed, err := applyPyPIYank(files, mutation)
	if err != nil {
		return nil, err
	}
	for _, file := range result {
		if _, err = tx.ExecContext(ctx, `UPDATE native_pypi_files SET yanked=$3,yank_reason=$4,yank_revision=$5 WHERE repository_id::text=$1 AND filename=$2`, file.RepositoryID, file.Filename, file.Yanked, file.YankReason, file.YankRevision); err != nil {
			return nil, err
		}
	}
	if err = insertAudit(ctx, tx, mutation.auditRecord(result, changed)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
