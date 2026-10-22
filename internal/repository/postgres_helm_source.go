package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) GetHelmSourceDocument(ctx context.Context, id, version string) (HelmSourceDocument, error) {
	var in HelmSourceDocument
	e := s.db.QueryRowContext(ctx, `SELECT d.repository_id::text,d.repository_version,d.body,d.digest,d.etag,d.last_modified,d.fetched_at,d.expires_at FROM native_helm_source_documents d JOIN hosted_repositories h ON h.id=d.repository_id WHERE h.id::text=$1 AND h.version::text=$2 AND h.format='helm' AND h.repo_type='proxy' AND h.state='active' AND d.repository_version=$2`, id, version).Scan(&in.RepositoryID, &in.RepositoryVersion, &in.Body, &in.Digest, &in.ETag, &in.LastModified, &in.FetchedAt, &in.ExpiresAt)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return in, e
}
func (s *PostgresStore) PutHelmSourceDocument(ctx context.Context, in HelmSourceDocument) error {
	if !validHelmDocument(in) {
		return ErrInvalidHelmPublication
	}
	tx, _, e := s.helmSourceTransaction(ctx, in.RepositoryID, in.RepositoryVersion)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	_, e = tx.ExecContext(ctx, `INSERT INTO native_helm_source_documents(repository_id,repository_version,body,digest,etag,last_modified,fetched_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(repository_id) DO UPDATE SET repository_version=EXCLUDED.repository_version,body=EXCLUDED.body,digest=EXCLUDED.digest,etag=EXCLUDED.etag,last_modified=EXCLUDED.last_modified,fetched_at=EXCLUDED.fetched_at,expires_at=EXCLUDED.expires_at`, in.RepositoryID, in.RepositoryVersion, in.Body, in.Digest, in.ETag, in.LastModified, in.FetchedAt, in.ExpiresAt)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *PostgresStore) helmSourceTransaction(ctx context.Context, id, version string) (*sql.Tx, string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, "", e
	}
	var current, name string
	e = tx.QueryRowContext(ctx, `SELECT name,version::text FROM hosted_repositories WHERE id::text=$1 AND format='helm' AND repo_type='proxy' AND state='active' FOR UPDATE`, id).Scan(&name, &current)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	if e == nil && current != version {
		e = ErrHelmSourceChanged
	}
	if e != nil {
		_ = tx.Rollback()
		return nil, "", e
	}
	return tx, name, nil
}
func checkHelmView(ctx context.Context, tx *sql.Tx, id, version string) error {
	var current string
	e := tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_repositories WHERE id::text=$1 AND format='helm' AND state='active' FOR SHARE`, id).Scan(&current)
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_groups WHERE id::text=$1 AND format='helm' FOR SHARE`, id).Scan(&current)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	if e == nil && current != version {
		return ErrHelmSourceChanged
	}
	return e
}
func scanHelmSnapshot(row helmScanner) (HelmSourceSnapshot, error) {
	var in HelmSourceSnapshot
	var body []byte
	e := row.Scan(&in.ID, &in.ViewID, &in.ViewVersion, &in.Digest, &body, &in.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(body, &in.Entries)
	}
	return in, e
}

const helmSnapshotColumns = `id::text,view_id::text,view_version,digest,entries_json,created_at`

func (s *PostgresStore) PutHelmSourceSnapshot(ctx context.Context, in HelmSourceSnapshot) (HelmSourceSnapshot, error) {
	if !validHelmSnapshot(in) {
		return HelmSourceSnapshot{}, ErrInvalidHelmPublication
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return HelmSourceSnapshot{}, e
	}
	defer func() { _ = tx.Rollback() }()
	if e = checkHelmView(ctx, tx, in.ViewID, in.ViewVersion); e != nil {
		return HelmSourceSnapshot{}, e
	}
	// Serialize the per-view bound and concurrent create-only identities.
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "helm-snapshot:"+in.ViewID); e != nil {
		return HelmSourceSnapshot{}, e
	}
	prior, e := scanHelmSnapshot(tx.QueryRowContext(ctx, `SELECT `+helmSnapshotColumns+` FROM native_helm_source_snapshots WHERE id::text=$1`, in.ID))
	if e == nil {
		if prior.ViewID != in.ViewID || prior.ViewVersion != in.ViewVersion || prior.Digest != in.Digest {
			return HelmSourceSnapshot{}, ErrHelmPublicationConflict
		}
		return prior, tx.Commit()
	}
	if !errors.Is(e, ErrNotFound) {
		return HelmSourceSnapshot{}, e
	}
	var count int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM native_helm_source_snapshots WHERE view_id::text=$1`, in.ViewID).Scan(&count)
	if e != nil {
		return HelmSourceSnapshot{}, e
	}
	if count >= 128 {
		return HelmSourceSnapshot{}, ErrHelmCacheLimit
	}
	for _, entry := range in.Entries {
		var v string
		e = tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_repositories WHERE id::text=$1 AND format='helm' AND state='active' FOR SHARE`, entry.RepositoryID).Scan(&v)
		if errors.Is(e, sql.ErrNoRows) || e == nil && v != entry.RepositoryVersion {
			return HelmSourceSnapshot{}, ErrHelmSourceChanged
		}
		if e != nil {
			return HelmSourceSnapshot{}, e
		}
	}
	body, _ := json.Marshal(in.Entries)
	out, e := scanHelmSnapshot(tx.QueryRowContext(ctx, `INSERT INTO native_helm_source_snapshots(id,view_id,view_version,digest,entries_json) VALUES($1,$2,$3,$4,$5) RETURNING `+helmSnapshotColumns, in.ID, in.ViewID, in.ViewVersion, in.Digest, string(body)))
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *PostgresStore) GetHelmSourceSnapshot(ctx context.Context, view, id string) (HelmSourceSnapshot, error) {
	return scanHelmSnapshot(s.db.QueryRowContext(ctx, `SELECT `+helmSnapshotColumns+` FROM native_helm_source_snapshots WHERE id::text=$1 AND view_id::text=$2`, id, view))
}
func scanHelmAuthority(row helmScanner) (HelmGroupAuthority, error) {
	var in HelmGroupAuthority
	var body []byte
	e := row.Scan(&in.GroupID, &body, &in.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(body, &in.Entry)
	}
	return in, e
}
func checkHelmAuthorityOwner(ctx context.Context, tx *sql.Tx, in HelmGroupAuthority) error {
	var v string
	e := tx.QueryRowContext(ctx, `SELECT h.version::text FROM hosted_repositories h WHERE h.id::text=$1 AND h.format='helm' AND h.state='active' AND EXISTS(SELECT 1 FROM hosted_group_members m WHERE m.group_id::text=$2 AND m.repository_id=h.id) FOR SHARE`, in.Entry.RepositoryID, in.GroupID).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) || e == nil && v != in.Entry.RepositoryVersion {
		return ErrHelmSourceChanged
	}
	return e
}
func (s *PostgresStore) BindHelmGroupAuthority(ctx context.Context, expected string, in HelmGroupAuthority) (HelmGroupAuthority, error) {
	if !validHelmSourceEntry(in.Entry) {
		return HelmGroupAuthority{}, ErrInvalidHelmPublication
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return HelmGroupAuthority{}, e
	}
	defer func() { _ = tx.Rollback() }()
	var v string
	e = tx.QueryRowContext(ctx, `SELECT version::text FROM hosted_groups WHERE id::text=$1 AND format='helm' FOR UPDATE`, in.GroupID).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return HelmGroupAuthority{}, ErrNotFound
	}
	if e != nil {
		return HelmGroupAuthority{}, e
	}
	if v != expected {
		return HelmGroupAuthority{}, ErrVersionConflict
	}
	prior, e := scanHelmAuthority(tx.QueryRowContext(ctx, `SELECT group_id::text,entry_json,created_at FROM native_helm_group_authorities WHERE group_id::text=$1 AND name=$2 AND version=$3`, in.GroupID, in.Entry.Name, in.Entry.Version))
	if e == nil {
		if e = checkHelmAuthorityOwner(ctx, tx, prior); e != nil {
			return HelmGroupAuthority{}, e
		}
		return prior, tx.Commit()
	}
	if !errors.Is(e, ErrNotFound) {
		return HelmGroupAuthority{}, e
	}
	if e = checkHelmAuthorityOwner(ctx, tx, in); e != nil {
		return HelmGroupAuthority{}, e
	}
	body, _ := json.Marshal(in.Entry)
	out, e := scanHelmAuthority(tx.QueryRowContext(ctx, `INSERT INTO native_helm_group_authorities(group_id,name,version,entry_json) VALUES($1,$2,$3,$4) RETURNING group_id::text,entry_json,created_at`, in.GroupID, in.Entry.Name, in.Entry.Version, string(body)))
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *PostgresStore) GetHelmGroupAuthority(ctx context.Context, group, name, version string) (HelmGroupAuthority, error) {
	return scanHelmAuthority(s.db.QueryRowContext(ctx, `SELECT group_id::text,entry_json,created_at FROM native_helm_group_authorities WHERE group_id::text=$1 AND name=$2 AND version=$3`, group, name, version))
}
