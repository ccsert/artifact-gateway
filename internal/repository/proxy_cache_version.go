package repository

import (
	"context"
	"database/sql"
	"errors"
)

// checkProxySourceVersion fences a cache publication against repository CAS.
// It must run inside the same transaction as the metadata/object reference.
func checkProxySourceVersion(ctx context.Context, tx *sql.Tx, repositoryID, version string) error {
	if version == "" {
		return ErrVersionConflict
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_repositories WHERE id::text=$1 AND repo_type='proxy' AND state='active' FOR SHARE`, repositoryID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if current != version {
		return ErrVersionConflict
	}
	return nil
}

// The memory adapter uses the same CAS fence while holding s.mu.
func (s *MemoryStore) checkProxySourceVersionLocked(repositoryID, version string) error {
	if version == "" {
		return ErrVersionConflict
	}
	repo, found := s.hostedRepositories[repositoryID]
	if !found || repo.Type != RepositoryTypeProxy || repo.State != RepositoryActive {
		return ErrNotFound
	}
	if repo.Version != version {
		return ErrVersionConflict
	}
	return nil
}

// A packument can change before a tarball is cached, even without a repository
// configuration change. Bytes verified against the old tuple cannot fill it.
func sameNPMProxyTarballMetadata(left, right NPMVersion) bool {
	return left.Integrity == right.Integrity && left.Shasum == right.Shasum &&
		left.UpstreamTarball == right.UpstreamTarball && left.TarballName == right.TarballName
}
