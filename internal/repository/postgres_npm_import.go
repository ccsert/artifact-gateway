package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) PublishNPMImport(ctx context.Context, c NPMImportClosure) error {
	if err := validateNPMImport(c); err != nil {
		return err
	}
	keys := make([]string, 0, len(c.Versions))
	for _, v := range c.Versions {
		keys = append(keys, artifactScanLockKey(c.RepositoryID, FormatNPM, c.PackageName+"@"+v.Version, v.Digest))
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
	if err = lockHostedNPMPackage(ctx, tx, c.RepositoryID, c.PackageName); err != nil {
		return err
	}
	var repoName string
	err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1 AND format='npm' AND repo_type='hosted' AND state='active' FOR SHARE`, c.RepositoryID).Scan(&repoName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisabled
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_npm_packages(repository_id,name,dist_tags)VALUES($1,$2,'{}'::jsonb)ON CONFLICT DO NOTHING`, c.RepositoryID, c.PackageName); err != nil {
		return err
	}
	pkg, err := readNPMPackage(ctx, tx, c.RepositoryID, c.PackageName)
	if err != nil {
		return err
	}
	if pkg.SourceEndpoint != "" || pkg.Negative {
		return ErrNameExists
	}
	for tag, want := range c.DistTags {
		if old, exists := pkg.DistTags[tag]; exists && old != want {
			return ErrNameExists
		}
	}
	oldVersions := map[string]NPMVersion{}
	for _, v := range pkg.Versions {
		oldVersions[v.Version] = v
	}
	for _, v := range pkg.TombstonedVersions {
		oldVersions[v.Version] = v
	}
	changed := false
	for _, v := range c.Versions {
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_tombstones WHERE repository_id::text=$1 AND format='npm' AND coordinate=$2) OR EXISTS(SELECT 1 FROM artifact_quarantines WHERE repository_id::text=$1 AND format='npm' AND coordinate=$2 AND digest=$3 AND state='quarantined')`, c.RepositoryID, c.PackageName+"@"+v.Version, v.Digest).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return ErrArtifactQuarantined
		}
		if old, exists := oldVersions[v.Version]; exists {
			if !NPMImportVersionMatches(old, v) {
				return ErrNameExists
			}
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_npm_versions(repository_id,package_name,version,digest,integrity,shasum,tarball_name,object_key,size,manifest,publisher,cached_at,deprecated)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,now(),$12)`, c.RepositoryID, c.PackageName, v.Version, v.Digest, v.Integrity, v.Shasum, v.TarballName, v.ObjectKey, v.Size, v.Manifest, v.Publisher, v.Deprecated)
		if isUnique(err) {
			return ErrNameExists
		}
		if err != nil {
			return err
		}
		changed = true
	}
	for tag, version := range c.DistTags {
		if _, exists := pkg.DistTags[tag]; !exists {
			changed = true
			pkg.DistTags[tag] = version
		}
	}
	if changed {
		tags, _ := json.Marshal(pkg.DistTags)
		if _, err = tx.ExecContext(ctx, `UPDATE native_npm_packages SET dist_tags=$3::jsonb,revision=revision+1,updated_at=now()WHERE repository_id::text=$1 AND name=$2`, c.RepositoryID, c.PackageName, tags); err != nil {
			return err
		}
		if err = insertAudit(ctx, tx, npmImportAudit(c, repoName)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
