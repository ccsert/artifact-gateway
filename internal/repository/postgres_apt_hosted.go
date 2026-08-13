package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const aptPublicationSessionColumns = `id::text,repository_id::text,suite,component,publisher,object_name,declared_digest,declared_size,expected_identity,object_key,COALESCE(package_revision_id::text,''),state,expires_at,created_at,reclaim_scheduled_at,collected_at`
const aptPackageRevisionColumns = `id::text,repository_id::text,package_name,version,architecture,canonical_identity,digest,object_key,size,object_name,publisher,created_at`
const aptRepositorySnapshotColumns = `id::text,repository_id::text,suite,sequence,state,release_digest,inrelease_digest,signer_identity,key_fingerprint,signature_algorithm,created_at,published_at`
const aptSnapshotAssetColumns = `a.snapshot_id::text,a.repository_id::text,a.path,a.digest,a.object_key,a.size,a.content_type`

func (s *PostgresStore) CreateAPTPublicationSessionIdempotently(ctx context.Context, session APTPublicationSession, actor, target, key, payload string) (APTPublicationSession, bool, error) {
	return s.createAPTPublicationSessionIdempotently(ctx, session, actor, target, key, payload, nil)
}

func (s *PostgresStore) CreateAPTPublicationSessionWithAuditIdempotently(ctx context.Context, session APTPublicationSession, actor, target, key, payload string, audit AuditRecord) (APTPublicationSession, bool, error) {
	return s.createAPTPublicationSessionIdempotently(ctx, session, actor, target, key, payload, &audit)
}

func (s *PostgresStore) createAPTPublicationSessionIdempotently(ctx context.Context, session APTPublicationSession, actor, target, key, payload string, audit *AuditRecord) (APTPublicationSession, bool, error) {
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
	if audit != nil {
		if err = insertAudit(ctx, tx, *audit); err != nil {
			return APTPublicationSession{}, false, err
		}
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
	var scheduledAt, collectedAt sql.NullTime
	err := row.Scan(&session.ID, &session.RepositoryID, &session.Suite, &session.Component, &session.Publisher,
		&session.ObjectName, &session.DeclaredDigest, &session.DeclaredSize, &session.ExpectedIdentity,
		&session.ObjectKey, &session.PackageRevisionID, &session.State, &session.ExpiresAt, &session.CreatedAt, &scheduledAt, &collectedAt)
	if err == nil && scheduledAt.Valid {
		session.ReclaimScheduledAt = scheduledAt.Time
	}
	if err == nil && collectedAt.Valid {
		session.CollectedAt = collectedAt.Time
	}
	return err
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
	return s.completeAPTPackageUpload(ctx, id, revision, nil)
}

func (s *PostgresStore) CompleteAPTPackageUploadWithAudit(ctx context.Context, id string, revision APTPackageRevision, audit AuditRecord) (APTPackageRevision, error) {
	return s.completeAPTPackageUpload(ctx, id, revision, &audit)
}

func (s *PostgresStore) completeAPTPackageUpload(ctx context.Context, id string, revision APTPackageRevision, audit *AuditRecord) (APTPackageRevision, error) {
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
	if audit != nil {
		if err = insertAudit(ctx, tx, *audit); err != nil {
			return APTPackageRevision{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return APTPackageRevision{}, err
	}
	return stored, nil
}

func scanAPTPublicationSessionWithActive(row interface{ Scan(...any) error }, session *APTPublicationSession, active *bool) error {
	var scheduledAt, collectedAt sql.NullTime
	err := row.Scan(&session.ID, &session.RepositoryID, &session.Suite, &session.Component, &session.Publisher,
		&session.ObjectName, &session.DeclaredDigest, &session.DeclaredSize, &session.ExpectedIdentity,
		&session.ObjectKey, &session.PackageRevisionID, &session.State, &session.ExpiresAt, &session.CreatedAt, &scheduledAt, &collectedAt, active)
	if err == nil && scheduledAt.Valid {
		session.ReclaimScheduledAt = scheduledAt.Time
	}
	if err == nil && collectedAt.Valid {
		session.CollectedAt = collectedAt.Time
	}
	return err
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
	FROM candidates c WHERE s.id=c.id RETURNING s.id::text,s.repository_id::text,s.object_key`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]APTAbandonedUpload, 0)
	for rows.Next() {
		var candidate APTAbandonedUpload
		if err = rows.Scan(&candidate.SessionID, &candidate.RepositoryID, &candidate.ObjectKey); err != nil {
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

func (s *PostgresStore) ListUncollectedAPTPublicationObjects(ctx context.Context, limit int) ([]APTAbandonedUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,object_key
		FROM native_apt_publication_sessions
		WHERE state='aborted' AND object_key<>'' AND collected_at IS NULL
		ORDER BY expires_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]APTAbandonedUpload, 0)
	for rows.Next() {
		var item APTAbandonedUpload
		if err = rows.Scan(&item.SessionID, &item.RepositoryID, &item.ObjectKey); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListUnscheduledAPTPublicationObjects(ctx context.Context, limit int) ([]APTAbandonedUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,object_key
		FROM native_apt_publication_sessions
		WHERE state='aborted' AND object_key<>'' AND reclaim_scheduled_at IS NULL AND collected_at IS NULL
		ORDER BY expires_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]APTAbandonedUpload, 0)
	for rows.Next() {
		var item APTAbandonedUpload
		if err = rows.Scan(&item.SessionID, &item.RepositoryID, &item.ObjectKey); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) MarkAPTPublicationObjectScheduled(ctx context.Context, sessionID, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_publication_sessions
		SET reclaim_scheduled_at=COALESCE(reclaim_scheduled_at,clock_timestamp())
		WHERE id::text=$1 AND state='aborted' AND object_key=$2 AND collected_at IS NULL`, sessionID, objectKey)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) MarkAPTPublicationObjectCollected(ctx context.Context, sessionID, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_publication_sessions
		SET collected_at=COALESCE(collected_at,clock_timestamp())
		WHERE id::text=$1 AND state='aborted' AND object_key=$2`, sessionID, objectKey)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) APTObjectHasPackageReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_apt_package_revisions WHERE object_key=$1)`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) CreateAPTRepositorySnapshot(ctx context.Context, snapshot APTRepositorySnapshot, items []APTSnapshotPackage) (APTRepositorySnapshot, error) {
	if snapshot.ID == "" || snapshot.RepositoryID == "" || !ValidAPTPublicationScope(snapshot.Suite) || snapshot.Sequence <= 0 || snapshot.State != APTRepositorySnapshotBuilding || len(items) == 0 {
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
		(id,repository_id,suite,sequence,state,release_digest,inrelease_digest,signer_identity,key_fingerprint,signature_algorithm,created_at,published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE($11,clock_timestamp()),NULL)
		RETURNING `+aptRepositorySnapshotColumns, snapshot.ID, snapshot.RepositoryID, snapshot.Suite, snapshot.Sequence,
		snapshot.State, snapshot.ReleaseDigest, snapshot.InReleaseDigest, snapshot.SignerIdentity, snapshot.KeyFingerprint, snapshot.SignatureAlgorithm, snapshotCreatedAt), &snapshot)
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

func (s *PostgresStore) CreateAPTSnapshotObjectIntents(ctx context.Context, snapshotID string, intents []APTSnapshotObjectIntent) error {
	if snapshotID == "" || len(intents) == 0 {
		return ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var snapshot APTRepositorySnapshot
	if err = scanAPTRepositorySnapshot(tx.QueryRowContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE id=$1 FOR UPDATE`, snapshotID), &snapshot); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if snapshot.State != APTRepositorySnapshotBuilding {
		return ErrVersionConflict
	}
	for _, intent := range intents {
		if !validAPTSnapshotObjectIntent(snapshot, intent) {
			return ErrDisabled
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_apt_snapshot_object_intents (snapshot_id,repository_id,object_key,digest,size)
			VALUES ($1,$2,$3,$4,$5) ON CONFLICT (snapshot_id,object_key) DO NOTHING`, intent.SnapshotID, intent.RepositoryID, intent.ObjectKey, intent.Digest, intent.Size)
		if insertErr != nil {
			return insertErr
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			var digest string
			var size int64
			if err = tx.QueryRowContext(ctx, `SELECT digest,size FROM native_apt_snapshot_object_intents WHERE snapshot_id=$1 AND object_key=$2`, snapshotID, intent.ObjectKey).Scan(&digest, &size); err != nil {
				return err
			}
			if digest != intent.Digest || size != intent.Size {
				return ErrVersionConflict
			}
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) PublishAPTRepositorySnapshotWithAudit(ctx context.Context, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	if !validAPTSnapshotPublication(snapshot, assets, release) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 FOR UPDATE`, snapshot.RepositoryID).Scan(new(string)); errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	} else if err != nil {
		return APTRepositorySnapshot{}, err
	}
	var current APTRepositorySnapshot
	if err = scanAPTRepositorySnapshot(tx.QueryRowContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE id=$1 FOR UPDATE`, snapshot.ID), &current); errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	} else if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if current.State != APTRepositorySnapshotBuilding || current.RepositoryID != snapshot.RepositoryID || current.Suite != snapshot.Suite || current.Sequence != snapshot.Sequence {
		return APTRepositorySnapshot{}, ErrVersionConflict
	}
	var visibleSequence int64
	err = tx.QueryRowContext(ctx, `SELECT sequence FROM native_apt_repository_snapshots WHERE repository_id=$1 AND suite=$2 AND state='visible' FOR UPDATE`, snapshot.RepositoryID, snapshot.Suite).Scan(&visibleSequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, err
	}
	if err == nil && visibleSequence >= snapshot.Sequence {
		return APTRepositorySnapshot{}, ErrVersionConflict
	}
	expectedPool := make(map[string]APTSnapshotAsset)
	packageRows, queryErr := tx.QueryContext(ctx, `SELECT sp.component,p.package_name,p.object_name,p.digest,p.object_key,p.size
		FROM native_apt_snapshot_packages sp JOIN native_apt_package_revisions p ON p.id=sp.package_revision_id
		WHERE sp.snapshot_id=$1`, snapshot.ID)
	if queryErr != nil {
		return APTRepositorySnapshot{}, queryErr
	}
	for packageRows.Next() {
		var component, packageName, objectName string
		var asset APTSnapshotAsset
		if err = packageRows.Scan(&component, &packageName, &objectName, &asset.Digest, &asset.ObjectKey, &asset.Size); err != nil {
			_ = packageRows.Close()
			return APTRepositorySnapshot{}, err
		}
		path := APTPoolPath(component, packageName, objectName)
		if _, duplicate := expectedPool[path]; duplicate {
			_ = packageRows.Close()
			return APTRepositorySnapshot{}, ErrDisabled
		}
		expectedPool[path] = asset
	}
	if err = packageRows.Err(); err != nil {
		_ = packageRows.Close()
		return APTRepositorySnapshot{}, err
	}
	_ = packageRows.Close()
	actualPool := 0
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Path, "pool/") {
			continue
		}
		actualPool++
		want, ok := expectedPool[asset.Path]
		if !ok || want.Digest != asset.Digest || want.ObjectKey != asset.ObjectKey || want.Size != asset.Size {
			return APTRepositorySnapshot{}, ErrDisabled
		}
	}
	if actualPool == 0 || actualPool != len(expectedPool) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Path, "pool/") {
			continue
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_apt_pool_paths (repository_id,path,digest,object_key,size,content_type)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (repository_id,path) DO NOTHING`, asset.RepositoryID, asset.Path, asset.Digest, asset.ObjectKey, asset.Size, asset.ContentType)
		if insertErr != nil {
			return APTRepositorySnapshot{}, insertErr
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			var digest, objectKey, contentType string
			var size int64
			if err = tx.QueryRowContext(ctx, `SELECT digest,object_key,size,content_type FROM native_apt_pool_paths WHERE repository_id=$1 AND path=$2`, asset.RepositoryID, asset.Path).Scan(&digest, &objectKey, &size, &contentType); err != nil {
				return APTRepositorySnapshot{}, err
			}
			if digest != asset.Digest || objectKey != asset.ObjectKey || size != asset.Size || contentType != asset.ContentType {
				return APTRepositorySnapshot{}, ErrAPTPackageConflict
			}
		}
	}
	for _, asset := range assets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_apt_snapshot_assets (snapshot_id,repository_id,path,digest,object_key,size,content_type) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			asset.SnapshotID, asset.RepositoryID, asset.Path, asset.Digest, asset.ObjectKey, asset.Size, asset.ContentType); isUnique(err) {
			return APTRepositorySnapshot{}, ErrNameExists
		} else if err != nil {
			return APTRepositorySnapshot{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_apt_repository_snapshots SET state='retired' WHERE repository_id=$1 AND suite=$2 AND state='visible'`, snapshot.RepositoryID, snapshot.Suite); err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = scanAPTRepositorySnapshot(tx.QueryRowContext(ctx, `UPDATE native_apt_repository_snapshots
		SET state='visible',release_digest=$2,inrelease_digest=$3,signer_identity=$4,key_fingerprint=$5,signature_algorithm=$6,published_at=clock_timestamp()
		WHERE id=$1 AND state='building' RETURNING `+aptRepositorySnapshotColumns,
		snapshot.ID, snapshot.ReleaseDigest, snapshot.InReleaseDigest, snapshot.SignerIdentity, snapshot.KeyFingerprint, snapshot.SignatureAlgorithm), &snapshot); err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return APTRepositorySnapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) FailAPTRepositorySnapshot(ctx context.Context, snapshotID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_repository_snapshots SET state='failed' WHERE id=$1 AND state='building'`, snapshotID)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	var state APTRepositorySnapshotState
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM native_apt_repository_snapshots WHERE id=$1`, snapshotID).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state == APTRepositorySnapshotFailed {
		return nil
	}
	return ErrVersionConflict
}

func (s *PostgresStore) ExpireAPTRepositorySnapshots(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	_, err := s.db.ExecContext(ctx, `WITH expired AS (
		SELECT s.id FROM native_apt_repository_snapshots s
		WHERE s.state='building'
		  AND EXISTS (SELECT 1 FROM native_apt_snapshot_object_intents i WHERE i.snapshot_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM native_apt_snapshot_object_intents i WHERE i.snapshot_id=s.id AND i.created_at>$1)
		ORDER BY s.id LIMIT $2 FOR UPDATE OF s SKIP LOCKED
	) UPDATE native_apt_repository_snapshots s SET state='failed' FROM expired e WHERE s.id=e.id`, before, limit)
	return err
}

func (s *PostgresStore) ListUnscheduledAPTSnapshotObjects(ctx context.Context, limit int) ([]APTSnapshotObjectIntent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.snapshot_id::text,i.repository_id::text,i.object_key,i.digest,i.size,i.created_at,i.reclaim_scheduled_at,i.collected_at
		FROM native_apt_snapshot_object_intents i JOIN native_apt_repository_snapshots s ON s.id=i.snapshot_id
		WHERE s.state='failed' AND i.reclaim_scheduled_at IS NULL AND i.collected_at IS NULL
		ORDER BY i.snapshot_id,i.object_key LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]APTSnapshotObjectIntent, 0)
	for rows.Next() {
		var intent APTSnapshotObjectIntent
		var scheduledAt, collectedAt sql.NullTime
		if err = rows.Scan(&intent.SnapshotID, &intent.RepositoryID, &intent.ObjectKey, &intent.Digest, &intent.Size, &intent.CreatedAt, &scheduledAt, &collectedAt); err != nil {
			return nil, err
		}
		if scheduledAt.Valid {
			intent.ScheduledAt = scheduledAt.Time
		}
		if collectedAt.Valid {
			intent.CollectedAt = collectedAt.Time
		}
		result = append(result, intent)
	}
	return result, rows.Err()
}

func (s *PostgresStore) MarkAPTSnapshotObjectScheduled(ctx context.Context, snapshotID, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_snapshot_object_intents i SET reclaim_scheduled_at=COALESCE(reclaim_scheduled_at,clock_timestamp())
		FROM native_apt_repository_snapshots s WHERE i.snapshot_id=$1 AND i.object_key=$2 AND s.id=i.snapshot_id AND s.state='failed' AND i.collected_at IS NULL`, snapshotID, objectKey)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) MarkAPTSnapshotObjectCollected(ctx context.Context, snapshotID, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_snapshot_object_intents i SET collected_at=COALESCE(collected_at,clock_timestamp())
		FROM native_apt_repository_snapshots s WHERE i.snapshot_id=$1 AND i.object_key=$2 AND s.id=i.snapshot_id AND s.state='failed'`, snapshotID, objectKey)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (s *PostgresStore) APTObjectHasDurableReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM native_apt_package_revisions WHERE object_key=$1)
		OR EXISTS (SELECT 1 FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id WHERE a.object_key=$1 AND s.state<>'failed')
		OR EXISTS (SELECT 1 FROM native_apt_snapshot_object_intents i JOIN native_apt_repository_snapshots s ON s.id=i.snapshot_id WHERE i.object_key=$1 AND s.state<>'failed')`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) GetVisibleAPTRepositorySnapshot(ctx context.Context, repositoryID, suite string) (APTRepositorySnapshot, error) {
	var snapshot APTRepositorySnapshot
	err := scanAPTRepositorySnapshot(s.db.QueryRowContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE repository_id::text=$1 AND suite=$2 AND state='visible'`, repositoryID, suite), &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	return snapshot, err
}

func (s *PostgresStore) GetVisibleAPTSnapshotAsset(ctx context.Context, repositoryID, path string) (APTSnapshotAsset, error) {
	var asset APTSnapshotAsset
	err := scanAPTSnapshotAsset(s.db.QueryRowContext(ctx, `SELECT `+aptSnapshotAssetColumns+`
		FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id
		WHERE a.repository_id::text=$1 AND a.path=$2 AND s.state='visible'
		ORDER BY s.sequence DESC,s.id DESC LIMIT 1`, repositoryID, path), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return APTSnapshotAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *PostgresStore) ListVisibleAPTSnapshotAssets(ctx context.Context, repositoryID, suite string) ([]APTSnapshotAsset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+aptSnapshotAssetColumns+`
		FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id
		WHERE a.repository_id::text=$1 AND s.suite=$2 AND s.state='visible' ORDER BY a.path`, repositoryID, suite)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	assets := make([]APTSnapshotAsset, 0)
	for rows.Next() {
		var asset APTSnapshotAsset
		if err = scanAPTSnapshotAsset(rows, &asset); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, ErrNotFound
	}
	return assets, nil
}

func scanAPTSnapshotAsset(row interface{ Scan(...any) error }, asset *APTSnapshotAsset) error {
	return row.Scan(&asset.SnapshotID, &asset.RepositoryID, &asset.Path, &asset.Digest, &asset.ObjectKey, &asset.Size, &asset.ContentType)
}

func scanAPTRepositorySnapshot(row interface{ Scan(...any) error }, snapshot *APTRepositorySnapshot) error {
	var publishedAt sql.NullTime
	err := row.Scan(&snapshot.ID, &snapshot.RepositoryID, &snapshot.Suite, &snapshot.Sequence, &snapshot.State,
		&snapshot.ReleaseDigest, &snapshot.InReleaseDigest, &snapshot.SignerIdentity, &snapshot.KeyFingerprint,
		&snapshot.SignatureAlgorithm, &snapshot.CreatedAt, &publishedAt)
	if err == nil && publishedAt.Valid {
		snapshot.PublishedAt = publishedAt.Time
	}
	return err
}

var _ NativeAPTPublicationStore = (*PostgresStore)(nil)
