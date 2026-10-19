package repository

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Every Hosted package mutation takes this transaction-scoped lock before
// version/package rows. It also fences publication of a new version not yet
// present when a full-packument mutation starts. Object leases remain outside
// this lock, in the same order used by publish and restore.
func lockHostedNPMPackage(ctx context.Context, tx *sql.Tx, repositoryID, name string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "native-npm-hosted-package:"+repositoryID+":"+name)
	return err
}

func (s *PostgresStore) MarkNPMObjectCollecting(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_npm_versions SET collecting_at=COALESCE(collecting_at,now())
	 WHERE object_key=$1 AND state='deleted' AND collected_at IS NULL
	 AND NOT EXISTS(SELECT 1 FROM native_npm_versions v WHERE v.object_key=$1 AND v.state='visible')`, objectKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) UpdateNPMMetadata(ctx context.Context, mutation NPMMetadataMutation) (NPMMetadataChange, error) {
	versions, err := mutation.selectedVersions()
	if err != nil {
		return NPMMetadataChange{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMMetadataChange{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockHostedNPMPackage(ctx, tx, mutation.RepositoryID, mutation.PackageName); err != nil {
		return NPMMetadataChange{}, err
	}
	pkg, err := readNPMPackage(ctx, tx, mutation.RepositoryID, mutation.PackageName)
	if err != nil {
		return NPMMetadataChange{}, err
	}
	if pkg.SourceEndpoint != "" || pkg.Negative {
		return NPMMetadataChange{}, ErrNotFound
	}
	if pkg.Revision != mutation.ExpectedRevision {
		return NPMMetadataChange{}, ErrVersionConflict
	}
	visible := make(map[string]NPMVersion, len(pkg.Versions))
	selected := make(map[string]bool, len(versions))
	for _, version := range pkg.Versions {
		visible[version.Version] = version
	}
	for _, version := range versions {
		if _, ok := visible[version]; !ok {
			return NPMMetadataChange{}, ErrNotFound
		}
		selected[version] = true
	}
	if mutation.Unpublish != nil {
		for _, target := range mutation.DistTags {
			if _, ok := visible[target]; !ok || selected[target] {
				return NPMMetadataChange{}, ErrNPMDistTagTargetUnavailable
			}
		}
	}
	change := NPMMetadataChange{Revision: pkg.Revision, Versions: versions}
	if mutation.Deprecations != nil {
		for _, version := range versions {
			message := mutation.Deprecations[version]
			if visible[version].Deprecated == message {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE native_npm_versions SET deprecated=$4 WHERE repository_id::text=$1 AND package_name=$2 AND version=$3`, mutation.RepositoryID, mutation.PackageName, version, message); err != nil {
				return NPMMetadataChange{}, err
			}
			change.Changed = true
		}
	} else {
		for _, version := range versions {
			if _, err = tombstoneNPMVersionTx(ctx, tx, mutation.RepositoryID, mutation.PackageName, version); err != nil {
				return NPMMetadataChange{}, err
			}
		}
		change.Changed = true
	}
	if change.Changed {
		if mutation.Unpublish != nil {
			tags, marshalErr := json.Marshal(mutation.DistTags)
			if marshalErr != nil {
				return NPMMetadataChange{}, marshalErr
			}
			err = tx.QueryRowContext(ctx, `UPDATE native_npm_packages SET dist_tags=$3::jsonb,revision=revision+1,updated_at=now() WHERE repository_id::text=$1 AND name=$2 RETURNING revision`, mutation.RepositoryID, mutation.PackageName, tags).Scan(&change.Revision)
		} else {
			err = tx.QueryRowContext(ctx, `UPDATE native_npm_packages SET revision=revision+1,updated_at=now() WHERE repository_id::text=$1 AND name=$2 RETURNING revision`, mutation.RepositoryID, mutation.PackageName).Scan(&change.Revision)
		}
		if err != nil {
			return NPMMetadataChange{}, err
		}
	}
	if err = insertAudit(ctx, tx, mutation.auditRecord(change)); err != nil {
		return NPMMetadataChange{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMMetadataChange{}, err
	}
	return change, nil
}
