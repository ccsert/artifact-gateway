package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

const nuGetSessionColumns = `id::text,repository_id::text,package_id,version,digest,size,actor,state,object_key,metadata_json,nuspec,created_at,expires_at,committed_at,collected_at`
const nuGetPublicationColumns = `repository_id::text,package_id,version,session_id::text,digest,object_key,size,metadata_json,nuspec,publisher,published_at`

type nuGetScanner interface{ Scan(...any) error }

func scanNuGetSession(row nuGetScanner) (NuGetSession, error) {
	var v NuGetSession
	var committed sql.NullTime
	var collected sql.NullTime
	err := row.Scan(&v.ID, &v.RepositoryID, &v.PackageID, &v.Version, &v.Digest, &v.Size, &v.Actor, &v.State, &v.ObjectKey, &v.MetadataJSON, &v.Nuspec, &v.CreatedAt, &v.ExpiresAt, &committed, &collected)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if committed.Valid {
		v.CommittedAt = committed.Time
	}
	if collected.Valid {
		v.CollectedAt = collected.Time
	}
	return v, err
}
func scanNuGetPublication(row nuGetScanner) (NuGetPublication, error) {
	var v NuGetPublication
	err := row.Scan(&v.RepositoryID, &v.PackageID, &v.Version, &v.SessionID, &v.Digest, &v.ObjectKey, &v.Size, &v.MetadataJSON, &v.Nuspec, &v.Publisher, &v.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func (s *PostgresStore) nuGetTransaction(ctx context.Context, repo string) (*sql.Tx, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	var name string
	err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1 AND format='nuget' AND repo_type='hosted' AND state='active' FOR UPDATE`, repo).Scan(&name)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, "", err
	}
	return tx, name, nil
}

func nuGetQuota(ctx context.Context, tx *sql.Tx, repo string, extra int64) error {
	var quota int64
	err := tx.QueryRowContext(ctx, `SELECT quota_bytes FROM repository_capacity_quotas WHERE repository_id::text=$1 FOR UPDATE`, repo).Scan(&quota)
	if errors.Is(err, sql.ErrNoRows) || err == nil && quota == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var used int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(size) FROM native_nuget_publications WHERE repository_id::text=$1),0)+COALESCE((SELECT SUM(size) FROM native_nuget_publication_sessions WHERE repository_id::text=$1 AND state IN ('reserved','staged') AND expires_at>now()),0)`, repo).Scan(&used)
	if err != nil {
		return err
	}
	if used > quota || extra > quota-used {
		return ErrQuotaExceeded
	}
	return nil
}

func (s *PostgresStore) ReserveNuGetPublication(ctx context.Context, in NuGetSession) (NuGetSession, bool, error) {
	if !validNuGetSession(in) || !in.ExpiresAt.After(time.Now()) || !in.CreatedAt.IsZero() || in.State != "" || in.ObjectKey != "" {
		return NuGetSession{}, false, ErrInvalidNuGetPublication
	}
	tx, _, err := s.nuGetTransaction(ctx, in.RepositoryID)
	if err != nil {
		return NuGetSession{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE native_nuget_publication_sessions SET state='expired' WHERE repository_id::text=$1 AND state IN ('reserved','staged') AND expires_at<=now()`, in.RepositoryID); err != nil {
		return NuGetSession{}, false, err
	}
	prior, err := scanNuGetSession(tx.QueryRowContext(ctx, `SELECT `+nuGetSessionColumns+` FROM native_nuget_publication_sessions WHERE id::text=$1`, in.ID))
	if err == nil {
		if !nuGetSessionSame(prior, in) || prior.Actor != in.Actor {
			return NuGetSession{}, false, ErrIdempotencyConflict
		}
		if prior.State == "expired" {
			return NuGetSession{}, false, ErrNuGetSessionExpired
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return NuGetSession{}, false, err
	}
	prior, err = scanNuGetSession(tx.QueryRowContext(ctx, `SELECT `+nuGetSessionColumns+` FROM native_nuget_publication_sessions WHERE repository_id::text=$1 AND package_id=$2 AND version=$3 AND state IN ('reserved','staged','committed')`, in.RepositoryID, in.PackageID, in.Version))
	if err == nil {
		if !nuGetSessionSame(prior, in) || prior.State != "committed" && prior.Actor != in.Actor {
			return NuGetSession{}, false, ErrNuGetPublicationConflict
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return NuGetSession{}, false, err
	}
	if err = nuGetQuota(ctx, tx, in.RepositoryID, in.Size); err != nil {
		return NuGetSession{}, false, err
	}
	in.ObjectKey = nuGetSessionObjectKey(in)
	in, err = scanNuGetSession(tx.QueryRowContext(ctx, `INSERT INTO native_nuget_publication_sessions(id,repository_id,package_id,version,digest,size,actor,state,object_key,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,'reserved',$8,$9) RETURNING `+nuGetSessionColumns, in.ID, in.RepositoryID, in.PackageID, in.Version, in.Digest, in.Size, in.Actor, in.ObjectKey, in.ExpiresAt))
	if err != nil {
		return NuGetSession{}, false, err
	}
	return in, false, tx.Commit()
}

func (s *PostgresStore) GetNuGetSession(ctx context.Context, id string) (NuGetSession, error) {
	return scanNuGetSession(s.db.QueryRowContext(ctx, `SELECT `+nuGetSessionColumns+` FROM native_nuget_publication_sessions WHERE id::text=$1 AND EXISTS(SELECT 1 FROM hosted_repositories h WHERE h.id=repository_id AND h.format='nuget' AND h.repo_type='hosted' AND h.state='active')`, id))
}

func (s *PostgresStore) StageNuGetPublication(ctx context.Context, in NuGetSession) (NuGetSession, error) {
	if !validNuGetStaging(in) {
		return NuGetSession{}, ErrInvalidNuGetPublication
	}
	tx, _, err := s.nuGetTransaction(ctx, in.RepositoryID)
	if err != nil {
		return NuGetSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	prior, err := scanNuGetSession(tx.QueryRowContext(ctx, `SELECT `+nuGetSessionColumns+` FROM native_nuget_publication_sessions WHERE id::text=$1 FOR UPDATE`, in.ID))
	if err != nil {
		return NuGetSession{}, err
	}
	if !nuGetSessionSame(prior, in) || prior.Actor != in.Actor {
		return NuGetSession{}, ErrNuGetPublicationConflict
	}
	if prior.State == "expired" || !prior.ExpiresAt.After(time.Now()) {
		return NuGetSession{}, ErrNuGetSessionExpired
	}
	if prior.State != "reserved" && prior.State != "staged" {
		return NuGetSession{}, ErrNuGetSessionState
	}
	if prior.State == "staged" && (!bytes.Equal(prior.MetadataJSON, in.MetadataJSON) || !bytes.Equal(prior.Nuspec, in.Nuspec)) {
		return NuGetSession{}, ErrNuGetPublicationConflict
	}
	prior, err = scanNuGetSession(tx.QueryRowContext(ctx, `UPDATE native_nuget_publication_sessions SET state='staged',metadata_json=$2,nuspec=$3 WHERE id::text=$1 RETURNING `+nuGetSessionColumns, in.ID, string(in.MetadataJSON), in.Nuspec))
	if err != nil {
		return NuGetSession{}, err
	}
	return prior, tx.Commit()
}

func (s *PostgresStore) CommitNuGetPublication(ctx context.Context, id, actor string) (NuGetPublication, bool, error) {
	before, err := s.GetNuGetSession(ctx, id)
	if err != nil {
		return NuGetPublication{}, false, err
	}
	tx, name, err := s.nuGetTransaction(ctx, before.RepositoryID)
	if err != nil {
		return NuGetPublication{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	session, err := scanNuGetSession(tx.QueryRowContext(ctx, `SELECT `+nuGetSessionColumns+` FROM native_nuget_publication_sessions WHERE id::text=$1 FOR UPDATE`, id))
	if err != nil {
		return NuGetPublication{}, false, err
	}
	if session.Actor != actor {
		return NuGetPublication{}, false, ErrNuGetPublicationConflict
	}
	if session.State == "committed" {
		p, err := scanNuGetPublication(tx.QueryRowContext(ctx, `SELECT `+nuGetPublicationColumns+` FROM native_nuget_publications WHERE session_id::text=$1`, id))
		if err != nil {
			return NuGetPublication{}, false, err
		}
		return p, true, tx.Commit()
	}
	if session.State == "expired" || !session.ExpiresAt.After(time.Now()) {
		return NuGetPublication{}, false, ErrNuGetSessionExpired
	}
	if session.State != "staged" {
		return NuGetPublication{}, false, ErrNuGetSessionState
	}
	if err = nuGetQuota(ctx, tx, session.RepositoryID, 0); err != nil {
		return NuGetPublication{}, false, err
	}
	var published time.Time
	if err = tx.QueryRowContext(ctx, `UPDATE native_nuget_publication_sessions SET state='committed',committed_at=now() WHERE id::text=$1 RETURNING committed_at`, id).Scan(&published); err != nil {
		return NuGetPublication{}, false, err
	}
	p, err := scanNuGetPublication(tx.QueryRowContext(ctx, `INSERT INTO native_nuget_publications(repository_id,package_id,version,session_id,digest,object_key,size,metadata_json,nuspec,publisher,published_at)SELECT repository_id,package_id,version,id,digest,object_key,size,metadata_json,nuspec,actor,committed_at FROM native_nuget_publication_sessions WHERE id::text=$1 RETURNING `+nuGetPublicationColumns, id))
	if err != nil {
		return NuGetPublication{}, false, err
	}
	if err = insertAudit(ctx, tx, nuGetPublicationAudit(p, name)); err != nil {
		return NuGetPublication{}, false, err
	}
	return p, false, tx.Commit()
}

func (s *PostgresStore) GetNuGetPublication(ctx context.Context, repo, id, version string) (NuGetPublication, error) {
	return scanNuGetPublication(s.db.QueryRowContext(ctx, `SELECT `+nuGetPublicationColumns+` FROM native_nuget_publications WHERE repository_id::text=$1 AND package_id=$2 AND version=$3 AND EXISTS(SELECT 1 FROM hosted_repositories h WHERE h.id=repository_id AND h.format='nuget' AND h.repo_type='hosted' AND h.state='active')`, repo, id, version))
}

func (s *PostgresStore) ListNuGetPublications(ctx context.Context, repo, id string, limit int, after string) ([]NuGetPublication, error) {
	if limit <= 0 || limit > 1000 {
		limit = 64
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1 AND format='nuget' AND repo_type='hosted' AND state='active')`, repo).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,package_id,version,session_id::text,digest,object_key,size,metadata_json,''::bytea,publisher,published_at FROM native_nuget_publications WHERE repository_id::text=$1 AND ($2='' OR package_id=$2) AND (package_id||'@'||version) COLLATE "C">$3 ORDER BY (package_id||'@'||version) COLLATE "C" LIMIT $4`, repo, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NuGetPublication{}
	for rows.Next() {
		p, err := scanNuGetPublication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ExpireNuGetSessions(ctx context.Context, now time.Time, limit int) ([]NuGetSession, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH expired AS(SELECT id FROM native_nuget_publication_sessions WHERE collected_at IS NULL AND (state='expired' OR state IN ('reserved','staged') AND expires_at<=$1) ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2) UPDATE native_nuget_publication_sessions SET state='expired' WHERE id IN(SELECT id FROM expired) RETURNING `+nuGetSessionColumns, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NuGetSession{}
	for rows.Next() {
		in, err := scanNuGetSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *PostgresStore) MarkNuGetSessionCollected(ctx context.Context, id, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_nuget_publication_sessions SET collected_at=COALESCE(collected_at,now()) WHERE id::text=$1 AND object_key=$2 AND state='expired'`, id, key)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrNuGetSessionState
	}
	return err
}

func (s *PostgresStore) LockNuGetPublication(ctx context.Context, repo, id, version string) (context.Context, func(), error) {
	return s.lockPostgresAdvisoryKeys(ctx, []string{"native-nuget-publication:" + repo + ":" + id + ":" + version})
}
