package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) GetNuGetSourceDocument(ctx context.Context, repo, version, url string) (NuGetSourceDocument, error) {
	var out NuGetSourceDocument
	err := s.db.QueryRowContext(ctx, `SELECT d.repository_id::text,d.source_version,d.url,d.status,d.body,d.digest,d.etag,d.last_modified,d.fetched_at,d.expires_at FROM native_nuget_source_documents d JOIN hosted_repositories h ON h.id=d.repository_id WHERE d.repository_id::text=$1 AND d.source_version=$2 AND d.url=$3 AND h.format='nuget' AND h.repo_type='proxy' AND h.state='active' AND h.version::text=d.source_version`, repo, version, url).Scan(&out.RepositoryID, &out.SourceVersion, &out.URL, &out.Status, &out.Body, &out.Digest, &out.ETag, &out.LastModified, &out.FetchedAt, &out.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}
func (s *PostgresStore) PutNuGetSourceDocument(ctx context.Context, in NuGetSourceDocument) error {
	if !validNuGetDocument(in) {
		return ErrInvalidNuGetPublication
	}
	tx, _, err := s.nuGetTransaction(ctx, in.RepositoryID, in.SourceVersion)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_nuget_source_documents WHERE repository_id::text=$1 AND source_version<>$2`, in.RepositoryID, in.SourceVersion); err != nil {
		return err
	}
	var count, total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(SUM(octet_length(body)),0) FROM native_nuget_source_documents WHERE repository_id::text=$1 AND url<>$2`, in.RepositoryID, in.URL).Scan(&count, &total); err != nil {
		return err
	}
	if count >= 4096 || total+int64(len(in.Body)) > 64<<20 {
		return ErrNuGetCacheLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_nuget_source_documents(repository_id,source_version,url,status,body,digest,etag,last_modified,fetched_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(repository_id,source_version,url) DO UPDATE SET status=EXCLUDED.status,body=EXCLUDED.body,digest=EXCLUDED.digest,etag=EXCLUDED.etag,last_modified=EXCLUDED.last_modified,fetched_at=EXCLUDED.fetched_at,expires_at=EXCLUDED.expires_at`, in.RepositoryID, in.SourceVersion, in.URL, in.Status, in.Body, in.Digest, in.ETag, in.LastModified, in.FetchedAt, in.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const nuGetAuthorityColumns = `group_id::text,package_id,version,repository_id::text,repository_version,digest,size,created_at`

func scanNuGetAuthority(row nuGetScanner) (NuGetGroupAuthority, error) {
	var in NuGetGroupAuthority
	err := row.Scan(&in.GroupID, &in.PackageID, &in.Version, &in.RepositoryID, &in.RepositoryVersion, &in.Digest, &in.Size, &in.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return in, err
}
func checkNuGetAuthorityOwner(ctx context.Context, tx *sql.Tx, in NuGetGroupAuthority) error {
	var version string
	err := tx.QueryRowContext(ctx, `SELECT h.version::text FROM hosted_repositories h WHERE h.id::text=$1 AND h.format='nuget' AND h.state='active' AND EXISTS(SELECT 1 FROM hosted_group_members m WHERE m.group_id::text=$2 AND m.repository_id=h.id) FOR SHARE`, in.RepositoryID, in.GroupID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) || err == nil && version != in.RepositoryVersion {
		return ErrNuGetSourceChanged
	}
	return err
}
func (s *PostgresStore) BindNuGetGroupAuthority(ctx context.Context, expected string, in NuGetGroupAuthority) (NuGetGroupAuthority, error) {
	if !validNuGetAuthority(in) {
		return NuGetGroupAuthority{}, ErrInvalidNuGetPublication
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var version string
	err = tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_groups WHERE id::text=$1 AND format='nuget' FOR UPDATE`, in.GroupID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	if version != expected {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	prior, err := scanNuGetAuthority(tx.QueryRowContext(ctx, `SELECT `+nuGetAuthorityColumns+` FROM native_nuget_group_authorities WHERE group_id::text=$1 AND package_id=$2 AND version=$3`, in.GroupID, in.PackageID, in.Version))
	if err == nil {
		if err = checkNuGetAuthorityOwner(ctx, tx, prior); err != nil {
			return NuGetGroupAuthority{}, err
		}
		if in.RepositoryID == prior.RepositoryID && in.RepositoryVersion == prior.RepositoryVersion && in.Digest != "" {
			if prior.Digest != "" && (prior.Digest != in.Digest || prior.Size != in.Size) {
				return NuGetGroupAuthority{}, ErrNuGetPublicationConflict
			}
			prior.Digest, prior.Size = in.Digest, in.Size
			if _, err = tx.ExecContext(ctx, `UPDATE native_nuget_group_authorities SET digest=$4,size=$5 WHERE group_id::text=$1 AND package_id=$2 AND version=$3`, in.GroupID, in.PackageID, in.Version, in.Digest, in.Size); err != nil {
				return NuGetGroupAuthority{}, err
			}
		}
		return prior, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return NuGetGroupAuthority{}, err
	}
	if err = checkNuGetAuthorityOwner(ctx, tx, in); err != nil {
		return NuGetGroupAuthority{}, err
	}
	in, err = scanNuGetAuthority(tx.QueryRowContext(ctx, `INSERT INTO native_nuget_group_authorities(group_id,package_id,version,repository_id,repository_version,digest,size) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+nuGetAuthorityColumns, in.GroupID, in.PackageID, in.Version, in.RepositoryID, in.RepositoryVersion, in.Digest, in.Size))
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	return in, tx.Commit()
}
func (s *PostgresStore) GetNuGetGroupAuthority(ctx context.Context, group, id, version string) (NuGetGroupAuthority, error) {
	return scanNuGetAuthority(s.db.QueryRowContext(ctx, `SELECT `+nuGetAuthorityColumns+` FROM native_nuget_group_authorities WHERE group_id::text=$1 AND package_id=$2 AND version=$3`, group, id, version))
}

func (s *PostgresStore) RevalidateNuGetGroupAuthority(ctx context.Context, groupVersion, priorVersion string, in NuGetGroupAuthority) (NuGetGroupAuthority, error) {
	if !validNuGetAuthority(in) || in.Digest == "" {
		return NuGetGroupAuthority{}, ErrInvalidNuGetPublication
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var current string
	err = tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_groups WHERE id::text=$1 AND format='nuget' FOR UPDATE`, in.GroupID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return NuGetGroupAuthority{}, ErrNotFound
	}
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	if current != groupVersion {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	prior, err := scanNuGetAuthority(tx.QueryRowContext(ctx, `SELECT `+nuGetAuthorityColumns+` FROM native_nuget_group_authorities WHERE group_id::text=$1 AND package_id=$2 AND version=$3`, in.GroupID, in.PackageID, in.Version))
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	if prior.RepositoryID != in.RepositoryID || prior.Digest == "" || prior.Digest != in.Digest || prior.Size != in.Size {
		return NuGetGroupAuthority{}, ErrNuGetPublicationConflict
	}
	if prior.RepositoryVersion != priorVersion && prior.RepositoryVersion != in.RepositoryVersion {
		return NuGetGroupAuthority{}, ErrVersionConflict
	}
	if err = checkNuGetAuthorityOwner(ctx, tx, in); err != nil {
		return NuGetGroupAuthority{}, err
	}
	prior, err = scanNuGetAuthority(tx.QueryRowContext(ctx, `UPDATE native_nuget_group_authorities SET repository_version=$4 WHERE group_id::text=$1 AND package_id=$2 AND version=$3 RETURNING `+nuGetAuthorityColumns, in.GroupID, in.PackageID, in.Version, in.RepositoryVersion))
	if err != nil {
		return NuGetGroupAuthority{}, err
	}
	return prior, tx.Commit()
}
