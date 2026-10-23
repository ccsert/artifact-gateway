package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func checkPyPIImportActorTx(ctx context.Context, tx *sql.Tx, actor string) error {
	var allowed bool
	switch {
	case strings.HasPrefix(actor, "user:"):
		err := tx.QueryRowContext(ctx, `SELECT role='admin' AND state='active' AND NOT must_change_password FROM users WHERE lower(name)=lower($1) FOR SHARE`, strings.TrimPrefix(actor, "user:")).Scan(&allowed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	case strings.HasPrefix(actor, "api-key:"):
		err := tx.QueryRowContext(ctx, `SELECT roles @> ARRAY['admin']::text[] AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND service_account_id IS NULL FROM api_keys WHERE id::text=$1 FOR SHARE`, strings.TrimPrefix(actor, "api-key:")).Scan(&allowed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	default:
		return ErrDisabled
	}
	if !allowed {
		return ErrDisabled
	}
	return nil
}
func (s *PostgresStore) CheckPyPIImportActor(ctx context.Context, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkPyPIImportActorTx(ctx, tx, actor); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *PostgresStore) PublishPyPIImport(ctx context.Context, c PyPIImportClosure) error {
	if err := validatePyPIImport(c); err != nil {
		return err
	}
	if _, ok := ctx.Value(nativePublicationGuardKey{}).(NativePublicationGuard); !ok {
		return ErrDisabled
	}
	coordinate := c.Project + "@" + c.Version
	ctx, releaseDistribution, err := LockArtifactDistributionCoordinates(ctx, s, []ArtifactDistributionCoordinate{{RepositoryID: c.RepositoryID, Format: FormatPyPI, Coordinate: coordinate}})
	if err != nil {
		return err
	}
	defer releaseDistribution()
	keys := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		keys = append(keys, artifactScanLockKey(c.RepositoryID, FormatPyPI, coordinate, f.Digest))
	}
	ctx, release, err := s.lockPostgresAdvisoryKeys(ctx, keys)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkNativePublicationGuard(ctx, tx); err != nil {
		return err
	}
	if err = checkPyPIImportActorTx(ctx, tx, c.Audit.Actor); err != nil {
		return err
	}
	var repoName string
	err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1 AND format='pypi' AND repo_type='hosted' AND state='active' FOR UPDATE`, c.RepositoryID).Scan(&repoName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisabled
	}
	if err != nil {
		return err
	}
	changed := false
	for _, f := range c.Files {
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_tombstones WHERE repository_id::text=$1 AND format='pypi' AND coordinate=$2) OR EXISTS(SELECT 1 FROM artifact_quarantines WHERE repository_id::text=$1 AND format='pypi' AND coordinate=$2 AND digest=$3 AND state='quarantined')`, c.RepositoryID, coordinate, f.Digest).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return ErrArtifactQuarantined
		}
		var old PyPIFile
		err = scanPyPIFile(tx.QueryRowContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files WHERE repository_id::text=$1 AND filename=$2 FOR UPDATE`, c.RepositoryID, f.Filename), &old)
		if err == nil {
			if !PyPIImportFileMatches(old, f) {
				return ErrNameExists
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_pypi_files(repository_id,project,version,filename,file_type,python_version,requires_python,digest,object_key,size,publisher,state,cached_at,yanked,yank_reason,yank_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'visible',now(),$12,$13,1)`, c.RepositoryID, c.Project, c.Version, f.Filename, f.FileType, f.PythonVersion, f.RequiresPython, f.Digest, f.ObjectKey, f.Size, f.Publisher, f.Yanked, f.YankReason)
		if isUnique(err) {
			return ErrNameExists
		}
		if err != nil {
			return err
		}
		changed = true
	}
	if changed {
		record := c.Audit
		record.Repository, record.GroupName, record.Format, record.Resource, record.Operation, record.Outcome = repoName, repoName, "pypi", coordinate, "publish", AuditResolved
		if err = insertAudit(ctx, tx, record); err != nil {
			return err
		}
	}
	// Expiry can cross while waiting for target/governance locks. Recheck under
	// the already held actor rows immediately before committing the checkpoint.
	if err = checkPyPIImportActorTx(ctx, tx, c.Audit.Actor); err != nil {
		return err
	}
	if err = completeNativePublication(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
