package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const aptPublicationSessionColumns = `id::text,repository_id::text,suite,component,publisher,object_name,declared_digest,declared_size,expected_identity,object_key,COALESCE(package_revision_id::text,''),state,expires_at,created_at`
const aptPackageRevisionColumns = `id::text,repository_id::text,package_name,version,architecture,canonical_identity,digest,object_key,size,object_name,publisher,created_at`
const aptRepositorySnapshotColumns = `id::text,repository_id::text,suite,sequence,state,release_digest,inrelease_digest,signer_identity,key_fingerprint,created_at,published_at`

func (s *PostgresStore) CreateAPTPublicationSessionIdempotently(ctx context.Context, session APTPublicationSession, actor, target, key, payload string) (APTPublicationSession, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTPublicationSession{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// The fast replay path is deliberately non-locking. All mutating paths below
	// acquire the stable repository row first, which keeps the lock graph ordered
	// repository -> idempotency -> quota.
	existingID, replayed, err := findActiveAPTPublicationReplay(ctx, tx, actor, target, key, payload, false)
	if err != nil {
		return APTPublicationSession{}, false, err
	}
	if replayed {
		if err = tx.Commit(); err != nil {
			return APTPublicationSession{}, false, err
		}
		existing, getErr := s.GetAPTPublicationSession(ctx, existingID)
		return existing, true, getErr
	}
	if !validAPTPublicationSession(session) || actor == "" || target == "" || key == "" || payload == "" {
		return APTPublicationSession{}, false, ErrDisabled
	}
	var repositoryID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 FOR UPDATE`, session.RepositoryID).Scan(&repositoryID); errors.Is(err, sql.ErrNoRows) {
		return APTPublicationSession{}, false, ErrNotFound
	} else if err != nil {
		return APTPublicationSession{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_publication_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at<=clock_timestamp()`, actor, target, key); err != nil {
		return APTPublicationSession{}, false, err
	}
	// A same-key request can commit while this transaction waits on the stable
	// repository row. Re-read inside the quota critical section so an exact
	// concurrent retry replays instead of being rejected by the first request's
	// reservation.
	existingID, replayed, err = findActiveAPTPublicationReplay(ctx, tx, actor, target, key, payload, true)
	if err != nil {
		return APTPublicationSession{}, false, err
	}
	if replayed {
		if err = tx.Commit(); err != nil {
			return APTPublicationSession{}, false, err
		}
		existing, getErr := s.GetAPTPublicationSession(ctx, existingID)
		return existing, true, getErr
	}
	var quota int64
	err = tx.QueryRowContext(ctx, `SELECT quota_bytes FROM repository_capacity_quotas WHERE repository_id=$1 FOR UPDATE`, session.RepositoryID).Scan(&quota)
	if errors.Is(err, sql.ErrNoRows) {
		quota = 0
	} else if err != nil {
		return APTPublicationSession{}, false, err
	}
	if quota > 0 {
		var used int64
		if err = tx.QueryRowContext(ctx, `SELECT
			COALESCE((SELECT sum(size) FROM native_apt_assets WHERE repository_id=$1),0)+
			COALESCE((SELECT sum(size) FROM native_apt_package_revisions WHERE repository_id=$1),0)+
			COALESCE((SELECT sum(declared_size) FROM native_apt_publication_sessions WHERE repository_id=$1 AND state IN ('open','uploading')),0)`, session.RepositoryID).Scan(&used); err != nil {
			return APTPublicationSession{}, false, err
		}
		if used+session.DeclaredSize > quota {
			return APTPublicationSession{}, false, ErrQuotaExceeded
		}
	}
	var createdAt any
	if !session.CreatedAt.IsZero() {
		createdAt = session.CreatedAt
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_apt_publication_sessions
		(id,repository_id,suite,component,publisher,object_name,declared_digest,declared_size,expected_identity,state,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,clock_timestamp()))`,
		session.ID, session.RepositoryID, session.Suite, session.Component, session.Publisher, session.ObjectName,
		session.DeclaredDigest, session.DeclaredSize, session.ExpectedIdentity, session.State, session.ExpiresAt, createdAt); err != nil {
		if isUnique(err) {
			return APTPublicationSession{}, false, ErrNameExists
		}
		return APTPublicationSession{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO native_apt_publication_idempotency
		(actor,target,key,payload_hash,session_id,expires_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp()+interval '24 hours')
		ON CONFLICT (actor,target,key) DO NOTHING`, actor, target, key, payload, session.ID)
	if err != nil {
		return APTPublicationSession{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		existingID, replayed, err = findActiveAPTPublicationReplay(ctx, tx, actor, target, key, payload, true)
		if err != nil {
			return APTPublicationSession{}, false, err
		}
		if !replayed {
			return APTPublicationSession{}, false, ErrNotFound
		}
		if err = tx.Rollback(); err != nil {
			return APTPublicationSession{}, false, err
		}
		existing, getErr := s.GetAPTPublicationSession(ctx, existingID)
		return existing, true, getErr
	}
	if err = tx.Commit(); err != nil {
		return APTPublicationSession{}, false, err
	}
	stored, getErr := s.GetAPTPublicationSession(ctx, session.ID)
	return stored, false, getErr
}

func findActiveAPTPublicationReplay(ctx context.Context, tx *sql.Tx, actor, target, key, payload string, lock bool) (string, bool, error) {
	var existingID, existingPayload string
	query := `SELECT session_id::text,payload_hash FROM native_apt_publication_idempotency
		WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at>clock_timestamp()`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRowContext(ctx, query, actor, target, key).Scan(&existingID, &existingPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if existingPayload != payload {
		return "", false, ErrIdempotencyConflict
	}
	return existingID, true, nil
}

func (s *PostgresStore) GetAPTPublicationSession(ctx context.Context, id string) (APTPublicationSession, error) {
	var session APTPublicationSession
	err := scanAPTPublicationSession(s.db.QueryRowContext(ctx, `SELECT `+aptPublicationSessionColumns+` FROM native_apt_publication_sessions WHERE id::text=$1`, id), &session)
	if errors.Is(err, sql.ErrNoRows) {
		return APTPublicationSession{}, ErrNotFound
	}
	return session, err
}

func scanAPTPublicationSession(row interface{ Scan(...any) error }, session *APTPublicationSession) error {
	return row.Scan(&session.ID, &session.RepositoryID, &session.Suite, &session.Component, &session.Publisher,
		&session.ObjectName, &session.DeclaredDigest, &session.DeclaredSize, &session.ExpectedIdentity,
		&session.ObjectKey, &session.PackageRevisionID, &session.State, &session.ExpiresAt, &session.CreatedAt)
}

func (s *PostgresStore) BeginAPTPackageUpload(ctx context.Context, id, objectKey string) error {
	if objectKey == "" {
		return ErrDisabled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_publication_sessions SET state='uploading',object_key=$2
		WHERE id::text=$1 AND expires_at>clock_timestamp() AND
		((state='open' AND object_key='') OR (state='uploading' AND object_key=$2))`, id, objectKey)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrDisabled
	}
	return nil
}

func (s *PostgresStore) CompleteAPTPackageUpload(ctx context.Context, id string, revision APTPackageRevision) (APTPackageRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTPackageRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var session APTPublicationSession
	var active bool
	err = scanAPTPublicationSessionWithActive(tx.QueryRowContext(ctx, `SELECT `+aptPublicationSessionColumns+`,expires_at>clock_timestamp() FROM native_apt_publication_sessions WHERE id::text=$1 FOR UPDATE`, id), &session, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return APTPackageRevision{}, ErrNotFound
	}
	if err != nil {
		return APTPackageRevision{}, err
	}
	if session.State == APTPublicationSessionStaged && session.PackageRevisionID != "" {
		stored, getErr := getAPTPackageRevisionTx(ctx, tx, session.PackageRevisionID)
		if getErr != nil {
			return APTPackageRevision{}, getErr
		}
		if err = tx.Commit(); err != nil {
			return APTPackageRevision{}, err
		}
		return stored, nil
	}
	if !active || session.State != APTPublicationSessionUploading || !validAPTPackageRevision(revision) ||
		revision.RepositoryID != session.RepositoryID || revision.Digest != session.DeclaredDigest ||
		revision.Size != session.DeclaredSize || revision.ObjectKey != session.ObjectKey || revision.ObjectName != session.ObjectName ||
		revision.CanonicalIdentity == "" || (session.ExpectedIdentity != "" && session.ExpectedIdentity != revision.CanonicalIdentity) {
		return APTPackageRevision{}, ErrDisabled
	}
	var revisionCreatedAt any
	if !revision.CreatedAt.IsZero() {
		revisionCreatedAt = revision.CreatedAt
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_package_revisions
		(id,repository_id,package_name,version,architecture,canonical_identity,digest,object_key,size,object_name,publisher,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,clock_timestamp()))
		ON CONFLICT (repository_id,canonical_identity) DO NOTHING`, revision.ID, revision.RepositoryID, revision.Package,
		revision.Version, revision.Architecture, revision.CanonicalIdentity, revision.Digest, revision.ObjectKey,
		revision.Size, revision.ObjectName, revision.Publisher, revisionCreatedAt)
	if err != nil {
		return APTPackageRevision{}, err
	}
	var stored APTPackageRevision
	err = scanAPTPackageRevision(tx.QueryRowContext(ctx, `SELECT `+aptPackageRevisionColumns+` FROM native_apt_package_revisions WHERE repository_id=$1 AND canonical_identity=$2 FOR UPDATE`, revision.RepositoryID, revision.CanonicalIdentity), &stored)
	if err != nil {
		return APTPackageRevision{}, err
	}
	if stored.Digest != revision.Digest || stored.ObjectKey != revision.ObjectKey || stored.Size != revision.Size {
		return APTPackageRevision{}, ErrAPTPackageConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE native_apt_publication_sessions SET state='staged',package_revision_id=$2 WHERE id=$1 AND state='uploading'`, id, stored.ID)
	if err != nil {
		return APTPackageRevision{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return APTPackageRevision{}, ErrDisabled
	}
	if err = tx.Commit(); err != nil {
		return APTPackageRevision{}, err
	}
	return stored, nil
}

func scanAPTPublicationSessionWithActive(row interface{ Scan(...any) error }, session *APTPublicationSession, active *bool) error {
	return row.Scan(&session.ID, &session.RepositoryID, &session.Suite, &session.Component, &session.Publisher,
		&session.ObjectName, &session.DeclaredDigest, &session.DeclaredSize, &session.ExpectedIdentity,
		&session.ObjectKey, &session.PackageRevisionID, &session.State, &session.ExpiresAt, &session.CreatedAt, active)
}

func scanAPTPackageRevision(row interface{ Scan(...any) error }, revision *APTPackageRevision) error {
	return row.Scan(&revision.ID, &revision.RepositoryID, &revision.Package, &revision.Version, &revision.Architecture,
		&revision.CanonicalIdentity, &revision.Digest, &revision.ObjectKey, &revision.Size, &revision.ObjectName,
		&revision.Publisher, &revision.CreatedAt)
}

func getAPTPackageRevisionTx(ctx context.Context, tx *sql.Tx, id string) (APTPackageRevision, error) {
	var revision APTPackageRevision
	err := scanAPTPackageRevision(tx.QueryRowContext(ctx, `SELECT `+aptPackageRevisionColumns+` FROM native_apt_package_revisions WHERE id::text=$1`, id), &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return APTPackageRevision{}, ErrNotFound
	}
	return revision, err
}

func (s *PostgresStore) GetAPTPackageRevisionForSession(ctx context.Context, sessionID string) (APTPackageRevision, error) {
	var revision APTPackageRevision
	err := scanAPTPackageRevision(s.db.QueryRowContext(ctx, `SELECT `+prefixedAPTColumns("p")+` FROM native_apt_publication_sessions s JOIN native_apt_package_revisions p ON p.id=s.package_revision_id WHERE s.id::text=$1`, sessionID), &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return APTPackageRevision{}, ErrNotFound
	}
	return revision, err
}

func prefixedAPTColumns(alias string) string {
	return alias + `.id::text,` + alias + `.repository_id::text,` + alias + `.package_name,` + alias + `.version,` +
		alias + `.architecture,` + alias + `.canonical_identity,` + alias + `.digest,` + alias + `.object_key,` +
		alias + `.size,` + alias + `.object_name,` + alias + `.publisher,` + alias + `.created_at`
}

func (s *PostgresStore) ExpireAPTPublicationSessions(ctx context.Context, before time.Time, limit int) ([]APTAbandonedUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH candidates AS MATERIALIZED (
		SELECT id FROM native_apt_publication_sessions
		WHERE state IN ('open','uploading') AND expires_at<=$1
		ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE native_apt_publication_sessions s SET state='aborted'
	FROM candidates c WHERE s.id=c.id RETURNING s.id::text,s.object_key`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]APTAbandonedUpload, 0)
	for rows.Next() {
		var candidate APTAbandonedUpload
		if err = rows.Scan(&candidate.SessionID, &candidate.ObjectKey); err != nil {
			return nil, err
		}
		if candidate.ObjectKey != "" {
			candidates = append(candidates, candidate)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	abandoned := make([]APTAbandonedUpload, 0, len(candidates))
	for _, candidate := range candidates {
		var referenced bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_apt_package_revisions WHERE object_key=$1)`, candidate.ObjectKey).Scan(&referenced); err != nil {
			return nil, err
		}
		if !referenced {
			abandoned = append(abandoned, candidate)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return abandoned, nil
}

func (s *PostgresStore) APTObjectHasPackageReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_apt_package_revisions WHERE object_key=$1)`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) CreateAPTRepositorySnapshot(ctx context.Context, snapshot APTRepositorySnapshot, items []APTSnapshotPackage) (APTRepositorySnapshot, error) {
	if snapshot.ID == "" || snapshot.RepositoryID == "" || !validAPTScopeSegment(snapshot.Suite) || snapshot.Sequence <= 0 || snapshot.State != APTRepositorySnapshotBuilding || len(items) == 0 {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	if err := validateAPTSnapshotMembership(items); err != nil {
		return APTRepositorySnapshot{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var snapshotCreatedAt any
	if !snapshot.CreatedAt.IsZero() {
		snapshotCreatedAt = snapshot.CreatedAt
	}
	err = scanAPTRepositorySnapshot(tx.QueryRowContext(ctx, `INSERT INTO native_apt_repository_snapshots
		(id,repository_id,suite,sequence,state,release_digest,inrelease_digest,signer_identity,key_fingerprint,created_at,published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE($10,clock_timestamp()),NULL)
		RETURNING `+aptRepositorySnapshotColumns, snapshot.ID, snapshot.RepositoryID, snapshot.Suite, snapshot.Sequence,
		snapshot.State, snapshot.ReleaseDigest, snapshot.InReleaseDigest, snapshot.SignerIdentity, snapshot.KeyFingerprint, snapshotCreatedAt), &snapshot)
	if isUnique(err) {
		return APTRepositorySnapshot{}, ErrNameExists
	}
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	for _, item := range items {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_apt_snapshot_packages (snapshot_id,publication_session_id,package_revision_id,component,architecture)
			SELECT $1,s.id,p.id,$4,$5
			FROM native_apt_publication_sessions s
			JOIN native_apt_package_revisions p ON p.id=s.package_revision_id
			WHERE s.id::text=$2 AND p.id::text=$3 AND s.repository_id=$6 AND s.state='staged'
			  AND s.suite=$7 AND s.component=$4 AND p.architecture=$5`, snapshot.ID, item.PublicationSessionID,
			item.PackageRevisionID, item.Component, item.Architecture, snapshot.RepositoryID, snapshot.Suite)
		if isUnique(insertErr) {
			return APTRepositorySnapshot{}, ErrNameExists
		}
		if insertErr != nil {
			return APTRepositorySnapshot{}, insertErr
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return APTRepositorySnapshot{}, ErrDisabled
		}
	}
	if err = tx.Commit(); err != nil {
		return APTRepositorySnapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) GetVisibleAPTRepositorySnapshot(ctx context.Context, repositoryID, suite string) (APTRepositorySnapshot, error) {
	var snapshot APTRepositorySnapshot
	err := scanAPTRepositorySnapshot(s.db.QueryRowContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE repository_id::text=$1 AND suite=$2 AND state='visible'`, repositoryID, suite), &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	return snapshot, err
}

func scanAPTRepositorySnapshot(row interface{ Scan(...any) error }, snapshot *APTRepositorySnapshot) error {
	var publishedAt sql.NullTime
	err := row.Scan(&snapshot.ID, &snapshot.RepositoryID, &snapshot.Suite, &snapshot.Sequence, &snapshot.State,
		&snapshot.ReleaseDigest, &snapshot.InReleaseDigest, &snapshot.SignerIdentity, &snapshot.KeyFingerprint,
		&snapshot.CreatedAt, &publishedAt)
	if err == nil && publishedAt.Valid {
		snapshot.PublishedAt = publishedAt.Time
	}
	return err
}

var _ NativeAPTPublicationStore = (*PostgresStore)(nil)
