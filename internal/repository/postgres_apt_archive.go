package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func loadAPTArchiveState(ctx context.Context, tx *sql.Tx, plan APTArchiveRestorePlan) (aptArchiveRestoreState, error) {
	state := aptArchiveRestoreState{packages: make(map[string]APTPackageRevision), snapshots: make(map[string]APTRepositorySnapshot), assets: make(map[string][]APTSnapshotAsset), pool: make(map[string]APTSnapshotAsset)}
	repoID := plan.Snapshot.RepositoryID
	err := tx.QueryRowContext(ctx, `SELECT id::text,name,format,repo_type,state FROM hosted_repositories WHERE id=$1 FOR UPDATE`, repoID).Scan(&state.repo.ID, &state.repo.Name, &state.repo.Format, &state.repo.Type, &state.repo.State)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrNotFound
	}
	if err != nil {
		return state, err
	}
	err = tx.QueryRowContext(ctx, `SELECT quota_bytes FROM repository_capacity_quotas WHERE repository_id=$1 FOR UPDATE`, repoID).Scan(&state.quota)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(size) FROM native_apt_assets WHERE repository_id=$1),0)+COALESCE((SELECT SUM(size) FROM native_apt_package_revisions WHERE repository_id=$1),0),
 COALESCE((SELECT SUM(declared_size) FROM native_apt_publication_sessions WHERE repository_id=$1 AND state IN ('open','uploading')),0)+COALESCE((SELECT SUM(reserved_bytes) FROM native_apt_archive_restores WHERE repository_id=$1 AND snapshot_id<>$2 AND state='preparing'),0)`, repoID, plan.Snapshot.ID).Scan(&state.baseBytes, &state.reservedBytes)
	if err != nil {
		return state, err
	}
	state.deletions, err = listAPTPackageDeletions(ctx, tx, repoID, plan.Snapshot.Suite)
	if err != nil {
		return state, err
	}
	identities := make([]string, 0, len(plan.Packages))
	paths := make([]string, 0, len(plan.Packages))
	for _, p := range plan.Packages {
		identities = append(identities, p.Revision.CanonicalIdentity)
		paths = append(paths, APTPoolPath(p.Component, p.Revision.Package, p.Revision.ObjectName))
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+aptPackageRevisionColumns+` FROM native_apt_package_revisions WHERE repository_id=$1 AND canonical_identity=ANY($2)`, repoID, identities)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var r APTPackageRevision
		if err = scanAPTPackageRevision(rows, &r); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.packages[r.CanonicalIdentity] = r
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return state, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE id=$2 OR (repository_id=$1 AND (state='visible' OR (suite=$3 AND sequence=$4)))`, repoID, plan.Snapshot.ID, plan.Snapshot.Suite, plan.Snapshot.Sequence)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var snapshot APTRepositorySnapshot
		if err = scanAPTRepositorySnapshot(rows, &snapshot); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.snapshots[snapshot.ID] = snapshot
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return state, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+aptSnapshotAssetColumns+` FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id WHERE a.repository_id=$1 AND (s.state='visible' OR a.snapshot_id=$2)`, repoID, plan.Snapshot.ID)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var a APTSnapshotAsset
		if err = scanAPTSnapshotAsset(rows, &a); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.assets[a.SnapshotID] = append(state.assets[a.SnapshotID], a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return state, err
	}
	members, err := listAPTSnapshotMembership(ctx, tx, plan.Snapshot.ID)
	if err != nil {
		return state, err
	}
	for _, m := range members {
		r, e := getAPTPackageRevisionTx(ctx, tx, m.PackageRevisionID)
		if e != nil {
			return state, e
		}
		state.members = append(state.members, APTArchivePackage{Component: m.Component, Revision: r})
	}
	rows, err = tx.QueryContext(ctx, `SELECT path,digest,object_key,size,content_type FROM native_apt_pool_paths WHERE repository_id=$1 AND path=ANY($2)`, repoID, paths)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var a APTSnapshotAsset
		if err = rows.Scan(&a.Path, &a.Digest, &a.ObjectKey, &a.Size, &a.ContentType); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.pool[a.Path] = a
	}
	err = rows.Err()
	_ = rows.Close()
	return state, err
}

func (s *PostgresStore) BeginAPTArchiveRestore(ctx context.Context, plan APTArchiveRestorePlan) error {
	if !validAPTArchivePlan(plan) {
		return ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadAPTArchiveState(ctx, tx, plan)
	if err != nil {
		return err
	}
	reserved, _, err := checkAPTArchiveRestore(plan, state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE native_apt_archive_restores SET state='failed',reserved_bytes=0,finished_at=clock_timestamp() WHERE repository_id=$1 AND snapshot_id=$2 AND state='preparing'`, plan.Snapshot.RepositoryID, plan.Snapshot.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_archive_restores(id,repository_id,snapshot_id,plan_digest,state,reserved_bytes) VALUES($1,$2,$3,$4,'preparing',$5)`, plan.ID, plan.Snapshot.RepositoryID, plan.Snapshot.ID, aptArchivePlanDigest(plan), reserved)
	if isUnique(err) {
		return ErrVersionConflict
	}
	if err != nil {
		return err
	}
	intents := aptArchiveObjectIntents(plan)
	keys := make([]string, 0, len(intents))
	digests := make([]string, 0, len(intents))
	sizes := make([]int64, 0, len(intents))
	for _, i := range intents {
		keys = append(keys, i.ObjectKey)
		digests = append(digests, i.Digest)
		sizes = append(sizes, i.Size)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_archive_object_intents(restore_id,repository_id,object_key,digest,size) SELECT $1,$2,o.key,o.digest,o.size FROM unnest($3::text[],$4::text[],$5::bigint[]) AS o(key,digest,size)`, plan.ID, plan.Snapshot.RepositoryID, keys, digests, sizes)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) CommitAPTArchiveRestore(ctx context.Context, plan APTArchiveRestorePlan, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	if !validAPTArchivePlan(plan) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	snapshot := plan.Snapshot
	snapshot.State = APTRepositorySnapshotVisible
	if !validAPTSnapshotPublication(snapshot, plan.Assets, release) {
		return APTRepositorySnapshot{}, ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadAPTArchiveState(ctx, tx, plan)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	var attemptState, digest string
	err = tx.QueryRowContext(ctx, `SELECT state,plan_digest FROM native_apt_archive_restores WHERE id=$1 AND repository_id=$2 FOR UPDATE`, plan.ID, snapshot.RepositoryID).Scan(&attemptState, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if attemptState != "preparing" || digest != aptArchivePlanDigest(plan) {
		return APTRepositorySnapshot{}, ErrVersionConflict
	}
	_, replay, err := checkAPTArchiveRestore(plan, state)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if !replay {
		if err = checkAPTAssetsAdmissionTx(ctx, tx, snapshot.RepositoryID, plan.Assets); err != nil {
			return APTRepositorySnapshot{}, err
		}
	}
	// Capacity reservation and metadata become durable in the same transaction.
	_, err = tx.ExecContext(ctx, `UPDATE native_apt_archive_restores SET state='completed',reserved_bytes=0,finished_at=clock_timestamp() WHERE id=$1`, plan.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if replay {
		snapshot = state.snapshots[snapshot.ID]
		if err = insertAudit(ctx, tx, audit); err != nil {
			return APTRepositorySnapshot{}, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_repository_snapshots(id,repository_id,suite,sequence,state,created_at) VALUES($1,$2,$3,$4,'building',$5)`, snapshot.ID, snapshot.RepositoryID, snapshot.Suite, snapshot.Sequence, snapshot.CreatedAt)
		if isUnique(err) {
			return APTRepositorySnapshot{}, ErrVersionConflict
		}
		if err != nil {
			return APTRepositorySnapshot{}, err
		}
		for _, p := range plan.Packages {
			r := p.Revision
			_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_package_revisions(id,repository_id,package_name,version,architecture,canonical_identity,digest,object_key,size,object_name,publisher,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(repository_id,canonical_identity) DO NOTHING`, r.ID, r.RepositoryID, r.Package, r.Version, r.Architecture, r.CanonicalIdentity, r.Digest, r.ObjectKey, r.Size, r.ObjectName, r.Publisher, r.CreatedAt)
			if err != nil {
				return APTRepositorySnapshot{}, err
			}
			var current APTPackageRevision
			if err = scanAPTPackageRevision(tx.QueryRowContext(ctx, `SELECT `+aptPackageRevisionColumns+` FROM native_apt_package_revisions WHERE repository_id=$1 AND canonical_identity=$2`, r.RepositoryID, r.CanonicalIdentity), &current); err != nil {
				return APTRepositorySnapshot{}, err
			}
			if !sameAPTArchiveRevision(current, r) {
				return APTRepositorySnapshot{}, ErrAPTPackageConflict
			}
			sessionID := uuid.NewString()
			_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_publication_sessions(id,repository_id,suite,component,publisher,object_name,declared_digest,declared_size,expected_identity,object_key,package_revision_id,state,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'staged',clock_timestamp()+interval '24 hours',$12)`, sessionID, r.RepositoryID, snapshot.Suite, p.Component, r.Publisher, r.ObjectName, r.Digest, r.Size, r.CanonicalIdentity, r.ObjectKey, current.ID, r.CreatedAt)
			if err != nil {
				return APTRepositorySnapshot{}, err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_snapshot_packages(snapshot_id,publication_session_id,package_revision_id,component,architecture) VALUES($1,$2,$3,$4,$5)`, snapshot.ID, sessionID, current.ID, p.Component, r.Architecture)
			if err != nil {
				return APTRepositorySnapshot{}, err
			}
		}
		published, publishErr := publishAPTRepositorySnapshotTx(ctx, tx, snapshot, plan.Assets, release, audit)
		if publishErr != nil {
			return APTRepositorySnapshot{}, publishErr
		}
		_, err = tx.ExecContext(ctx, `UPDATE native_apt_repository_snapshots SET created_at=$2,published_at=$3 WHERE id=$1`, snapshot.ID, snapshot.CreatedAt, snapshot.PublishedAt)
		if err != nil {
			return APTRepositorySnapshot{}, err
		}
		published.CreatedAt = snapshot.CreatedAt
		published.PublishedAt = snapshot.PublishedAt
		snapshot = published
	}
	if err = tx.Commit(); err != nil {
		return APTRepositorySnapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) FailAPTArchiveRestore(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_archive_restores SET state='failed',reserved_bytes=0,finished_at=COALESCE(finished_at,clock_timestamp()) WHERE id=$1 AND state IN ('preparing','failed')`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrVersionConflict
	}
	return nil
}
func (s *PostgresStore) ExpireAPTArchiveRestores(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,snapshot_id::text FROM native_apt_archive_restores WHERE state='preparing' AND created_at<$1 ORDER BY id LIMIT $2`, before, limit)
	if err != nil {
		return err
	}
	var items []APTArchiveRestore
	for rows.Next() {
		var r APTArchiveRestore
		if err = rows.Scan(&r.ID, &r.SnapshotID); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, r)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, r := range items {
		release, lockErr := s.LockAPTObject(ctx, "snapshot-lock/"+r.SnapshotID)
		if lockErr != nil {
			return lockErr
		}
		_, err = s.db.ExecContext(ctx, `UPDATE native_apt_archive_restores SET state='failed',reserved_bytes=0,finished_at=clock_timestamp() WHERE id=$1 AND state='preparing' AND created_at<$2`, r.ID, before)
		release()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *PostgresStore) ListUnscheduledAPTArchiveObjects(ctx context.Context, limit int) ([]APTArchiveObjectIntent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.restore_id::text,i.repository_id::text,i.object_key,i.digest,i.size FROM native_apt_archive_object_intents i JOIN native_apt_archive_restores r ON r.id=i.restore_id WHERE r.state='failed' AND i.reclaim_scheduled_at IS NULL AND i.collected_at IS NULL ORDER BY i.restore_id,i.object_key LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []APTArchiveObjectIntent
	for rows.Next() {
		var i APTArchiveObjectIntent
		if err = rows.Scan(&i.RestoreID, &i.RepositoryID, &i.ObjectKey, &i.Digest, &i.Size); err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}
func (s *PostgresStore) MarkAPTArchiveObjectScheduled(ctx context.Context, id, key string) error {
	return s.markAPTArchiveObject(ctx, id, key, "reclaim_scheduled_at")
}
func (s *PostgresStore) MarkAPTArchiveObjectCollected(ctx context.Context, id, key string) error {
	return s.markAPTArchiveObject(ctx, id, key, "collected_at")
}
func (s *PostgresStore) markAPTArchiveObject(ctx context.Context, id, key, column string) error {
	if column != "reclaim_scheduled_at" && column != "collected_at" {
		return ErrDisabled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_archive_object_intents i SET `+column+`=COALESCE(`+column+`,clock_timestamp()) FROM native_apt_archive_restores r WHERE i.restore_id=$1 AND i.object_key=$2 AND r.id=i.restore_id AND r.state='failed'`, id, key)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrVersionConflict
	}
	return nil
}

var _ NativeAPTArchiveRestoreStore = (*PostgresStore)(nil)
var _ NativeAPTArchiveRestoreStore = (*MemoryStore)(nil)
