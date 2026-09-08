package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type aptLifecycleQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listAPTPackageDeletions(ctx context.Context, q aptLifecycleQuerier, repoID, suite string) ([]APTPackageDeletion, error) {
	rows, err := q.QueryContext(ctx, `SELECT data FROM native_apt_package_deletions WHERE repository_id=$1 AND ($2='' OR suite=$2) ORDER BY (data->>'deletedAt')::timestamptz,id`, repoID, suite)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]APTPackageDeletion, 0)
	for rows.Next() {
		var body []byte
		var d APTPackageDeletion
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ListAPTPackageDeletions(ctx context.Context, repoID, suite string) ([]APTPackageDeletion, error) {
	return listAPTPackageDeletions(ctx, s.db, repoID, suite)
}
func (s *PostgresStore) ListAPTRepositorySnapshots(ctx context.Context, repoID, suite string) ([]APTSnapshotHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE repository_id=$1 AND suite=$2 ORDER BY sequence`, repoID, suite)
	if err != nil {
		return nil, err
	}
	out := make([]APTSnapshotHistory, 0)
	for rows.Next() {
		var h APTSnapshotHistory
		if err = scanAPTRepositorySnapshot(rows, &h.Snapshot); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, h)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id::text,retired_at FROM native_apt_repository_snapshots WHERE repository_id=$1 AND suite=$2 AND retired_at IS NOT NULL`, repoID, suite)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	times := make(map[string]time.Time)
	for rows.Next() {
		var id string
		var at time.Time
		if err = rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		times[id] = at
	}
	for i := range out {
		out[i].RetiredAt = times[out[i].Snapshot.ID]
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetAPTLifecycleResult(ctx context.Context, id string) (APTLifecycleResult, error) {
	var result APTLifecycleResult
	err := s.db.QueryRowContext(ctx, `SELECT request_digest,snapshot_id::text FROM native_apt_lifecycle_results WHERE id=$1`, id).Scan(&result.RequestDigest, &result.SnapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	return result, err
}
func writeAPTDeletion(ctx context.Context, tx *sql.Tx, d APTPackageDeletion) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_package_deletions(id,repository_id,suite,data) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data`, d.ID, d.RepositoryID, d.Suite, body)
	return err
}
func lockAPTLifecycleRepository(ctx context.Context, tx *sql.Tx, repoID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id=$1 AND format='apt' AND repo_type='hosted' AND state='active' FOR UPDATE`, repoID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s *PostgresStore) CommitAPTLifecycleSnapshot(ctx context.Context, plan APTLifecycleCommit, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAPTLifecycleRepository(ctx, tx, snapshot.RepositoryID); err != nil {
		return APTRepositorySnapshot{}, err
	}
	var base APTRepositorySnapshot
	if err = scanAPTRepositorySnapshot(tx.QueryRowContext(ctx, `SELECT `+aptRepositorySnapshotColumns+` FROM native_apt_repository_snapshots WHERE id=$1`, plan.BaseSnapshotID), &base); err != nil {
		return APTRepositorySnapshot{}, err
	}
	before, err := listAPTSnapshotMembership(ctx, tx, base.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	after, err := listAPTSnapshotMembership(ctx, tx, snapshot.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	deletions, err := listAPTPackageDeletions(ctx, tx, snapshot.RepositoryID, snapshot.Suite)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = validateAPTLifecycleCommit(plan, base, snapshot, before, after, deletions); err != nil {
		return APTRepositorySnapshot{}, err
	}
	for _, id := range plan.RestoreIDs {
		for _, d := range deletions {
			if d.ID == id {
				d.RestoredAt = plan.Now
				if err = writeAPTDeletion(ctx, tx, d); err != nil {
					return APTRepositorySnapshot{}, err
				}
			}
		}
	}
	result, err := publishAPTRepositorySnapshotTx(ctx, tx, snapshot, assets, release, audit)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	for _, id := range plan.RemoveSessionIDs {
		for _, m := range before {
			if m.PublicationSessionID == id {
				revision, e := getAPTPackageRevisionTx(ctx, tx, m.PackageRevisionID)
				if e != nil {
					return APTRepositorySnapshot{}, e
				}
				d := APTPackageDeletion{ID: uuid.NewSHA1(uuid.MustParse(plan.ID), []byte(id)).String(), RepositoryID: snapshot.RepositoryID, Suite: snapshot.Suite, Component: m.Component, SessionID: id, Revision: revision, DeletedAt: plan.Now, RestoreUntil: plan.Now.Add(APTPackageRecoveryPeriod)}
				if err = writeAPTDeletion(ctx, tx, d); err != nil {
					return APTRepositorySnapshot{}, err
				}
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_lifecycle_results(id,repository_id,request_digest,snapshot_id) VALUES($1,$2,$3,$4)`, plan.ID, snapshot.RepositoryID, plan.RequestDigest, snapshot.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return APTRepositorySnapshot{}, err
	}
	return result, nil
}

func (s *PostgresStore) PruneAPTSnapshots(ctx context.Context, repoID string, ids []string, now time.Time, audit AuditRecord) error {
	if len(ids) > 100 || now.IsZero() {
		return ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockAPTLifecycleRepository(ctx, tx, repoID); err != nil {
		return err
	}
	for _, id := range ids {
		var state APTRepositorySnapshotState
		var retired sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT state,retired_at FROM native_apt_repository_snapshots WHERE id=$1 AND repository_id=$2 FOR UPDATE`, id, repoID).Scan(&state, &retired)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state == APTRepositorySnapshotPruned {
			continue
		}
		if state != APTRepositorySnapshotRetired || !retired.Valid || now.Before(retired.Time.Add(APTSnapshotGracePeriod)) {
			return ErrVersionConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_snapshot_object_intents(snapshot_id,repository_id,object_key,digest,size,created_at) SELECT DISTINCT snapshot_id,repository_id,object_key,digest,size,$2::timestamptz FROM native_apt_snapshot_assets WHERE snapshot_id=$1 ON CONFLICT DO NOTHING`, id, now)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_snapshot_assets WHERE snapshot_id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_snapshot_packages WHERE snapshot_id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE native_apt_repository_snapshots SET state='pruned' WHERE id=$1`, id); err != nil {
			return err
		}
	}
	if err = purgeAPTDeletedPackagesTx(ctx, tx, repoID, now); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func purgeAPTDeletedPackagesTx(ctx context.Context, tx *sql.Tx, repoID string, now time.Time) error {
	deletions, err := listAPTPackageDeletions(ctx, tx, repoID, "")
	if err != nil {
		return err
	}
	for _, d := range deletions {
		if !d.RestoredAt.IsZero() || !d.PurgedAt.IsZero() || now.Before(d.RestoreUntil) {
			continue
		}
		protected := false
		for _, other := range deletions {
			if other.Revision.ID == d.Revision.ID && other.RestoredAt.IsZero() && other.PurgedAt.IsZero() && now.Before(other.RestoreUntil) {
				protected = true
			}
		}
		if protected {
			continue
		}
		var referenced bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_apt_snapshot_packages p JOIN native_apt_repository_snapshots s ON s.id=p.snapshot_id WHERE p.package_revision_id=$1 AND s.state NOT IN ('failed','pruned')) OR EXISTS(SELECT 1 FROM native_apt_publication_sessions WHERE package_revision_id=$1 AND created_at>$2)`, d.Revision.ID, now.Add(-APTSnapshotGracePeriod)).Scan(&referenced)
		if err != nil {
			return err
		}
		if referenced {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_snapshot_packages WHERE package_revision_id=$1`, d.Revision.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_publication_sessions WHERE package_revision_id=$1`, d.Revision.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_apt_package_revisions WHERE id=$1`, d.Revision.ID); err != nil {
			return err
		}
		d.PurgedAt = now
		if err = writeAPTDeletion(ctx, tx, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) ListUnscheduledAPTDeletionObjects(ctx context.Context, limit int) ([]APTPackageDeletion, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM native_apt_package_deletions WHERE (data->>'purgedAt')::timestamptz>'0001-01-01'::timestamptz AND reclaim_scheduled_at IS NULL AND collected_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]APTPackageDeletion, 0)
	for rows.Next() {
		var body []byte
		var d APTPackageDeletion
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *PostgresStore) MarkAPTDeletionObjectScheduled(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_package_deletions SET reclaim_scheduled_at=COALESCE(reclaim_scheduled_at,clock_timestamp()) WHERE id=$1 AND (data->>'purgedAt')::timestamptz>'0001-01-01'::timestamptz`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) MarkAPTDeletionObjectCollected(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_apt_package_deletions SET collected_at=COALESCE(collected_at,clock_timestamp()) WHERE id=$1 AND (data->>'purgedAt')::timestamptz>'0001-01-01'::timestamptz`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
