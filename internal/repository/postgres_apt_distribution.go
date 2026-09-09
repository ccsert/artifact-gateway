package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
)

func (s *PostgresStore) CommitAPTDistributionSnapshot(ctx context.Context, d APTDistributionCommit, snapshot APTRepositorySnapshot, assets []APTSnapshotAsset, release []byte, audit AuditRecord) (APTRepositorySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Consistent order also covers opposing A -> B and B -> A distribution.
	repos := []string{d.Source.RepositoryID, snapshot.RepositoryID}
	slices.Sort(repos)
	for _, id := range repos {
		if err = lockAPTLifecycleRepository(ctx, tx, id); err != nil {
			return APTRepositorySnapshot{}, err
		}
	}
	var workID string
	switch d.Operation {
	case "promote":
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM lifecycle_jobs WHERE id::text=$1 AND repository_id=$2 AND kind='promotion' AND state='running' AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND payload->>'format'='apt' AND payload->>'sourceRepositoryId'=$4 AND payload->>'coordinate'=$5 AND payload->>'digest'=$6 AND payload->>'aptTargetSuite'=$7 FOR UPDATE`, d.WorkID, snapshot.RepositoryID, d.LeaseToken, d.Source.RepositoryID, d.Source.Path, d.Source.Digest, snapshot.Suite).Scan(&workID)
	case "replicate":
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM replication_plans WHERE id::text=$1 AND target_repository_id=$2 AND state='running' AND lease_token=$3 AND lease_expires_at>clock_timestamp() AND source_repository_id=$4 AND format='apt' AND coordinate=$5 AND digest=$6 AND apt_target_suite=$7 FOR UPDATE`, d.WorkID, snapshot.RepositoryID, d.LeaseToken, d.Source.RepositoryID, d.Source.Path, d.Source.Digest, snapshot.Suite).Scan(&workID)
	default:
		return APTRepositorySnapshot{}, ErrDisabled
	}
	if errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	var visible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id WHERE a.repository_id=$1 AND a.path=$2 AND a.digest=$3 AND a.object_key=$4 AND a.size=$5 AND s.state='visible')`, d.Source.RepositoryID, d.Source.Path, d.Source.Digest, d.Source.ObjectKey, d.Source.Size).Scan(&visible)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if !visible {
		return APTRepositorySnapshot{}, ErrNotFound
	}
	if err = checkAPTAssetsAdmissionTx(ctx, tx, d.Source.RepositoryID, []APTSnapshotAsset{d.Source}); err != nil {
		return APTRepositorySnapshot{}, err
	}
	var baseID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM native_apt_repository_snapshots WHERE repository_id=$1 AND suite=$2 AND state='visible'`, snapshot.RepositoryID, snapshot.Suite).Scan(&baseID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return APTRepositorySnapshot{}, err
	}
	var before []APTSnapshotPackage
	if baseID != "" {
		before, err = listAPTSnapshotMembership(ctx, tx, baseID)
		if err != nil {
			return APTRepositorySnapshot{}, err
		}
	}
	after, err := listAPTSnapshotMembership(ctx, tx, snapshot.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = validateAPTDistributionCommit(d, snapshot, baseID, before, after, assets); err != nil {
		return APTRepositorySnapshot{}, err
	}
	result, err := publishAPTRepositorySnapshotTx(ctx, tx, snapshot, assets, release, audit)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_apt_lifecycle_results(id,repository_id,request_digest,snapshot_id) VALUES($1,$2,$3,$4)`, d.ID, snapshot.RepositoryID, d.RequestDigest, snapshot.ID)
	if err != nil {
		return APTRepositorySnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return APTRepositorySnapshot{}, err
	}
	return result, nil
}
