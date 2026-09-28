package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *PostgresStore) cargoObjectKey(ctx context.Context, repositoryID, name, version string) (string, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return "", ErrInvalidCargoIdentity
	}
	var key string
	err = s.db.QueryRowContext(ctx, `SELECT object_key FROM native_cargo_publications
		WHERE repository_id::text=$1 AND collision_key=$2 AND version_key=$3`,
		repositoryID, identity.CollisionKey, identity.VersionKey).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return key, err
}

func (s *PostgresStore) TombstoneCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	objectKey, err := s.cargoObjectKey(ctx, repositoryID, name, version)
	if err != nil {
		return CargoPublication{}, err
	}
	release, err := s.LockCargoObject(ctx, objectKey)
	if err != nil {
		return CargoPublication{}, err
	}
	defer release()
	identity, _ := cargo.NormalizeIdentity(name, version)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CargoPublication{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var publication CargoPublication
	err = scanCargoPublication(tx.QueryRowContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key=$3 AND p.collected_at IS NULL
		FOR UPDATE OF p`, repositoryID, identity.CollisionKey, identity.VersionKey), &publication)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, ErrNotFound
	}
	if err != nil {
		return CargoPublication{}, err
	}
	if publication.ObjectKey != objectKey {
		return CargoPublication{}, ErrUpstreamChanged
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest)
		VALUES ($1,'cargo',$2,$3) ON CONFLICT (repository_id,format,coordinate) DO NOTHING`,
		repositoryID, cargoPublicationCoordinate(publication.Name, publication.Version), publication.Digest)
	if err != nil {
		return CargoPublication{}, err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return CargoPublication{}, rowsErr
	} else if affected == 0 {
		return CargoPublication{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return CargoPublication{}, err
	}
	return publication, nil
}

func (s *PostgresStore) RestoreCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	objectKey, err := s.cargoObjectKey(ctx, repositoryID, name, version)
	if errors.Is(err, ErrNotFound) {
		return CargoPublication{}, ErrDisabled
	}
	if err != nil {
		return CargoPublication{}, err
	}
	release, err := s.LockCargoObject(ctx, objectKey)
	if err != nil {
		return CargoPublication{}, err
	}
	defer release()
	identity, _ := cargo.NormalizeIdentity(name, version)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CargoPublication{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var publication CargoPublication
	err = scanCargoPublication(tx.QueryRowContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		JOIN artifact_tombstones t ON t.repository_id=p.repository_id AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key=$3
		FOR UPDATE OF p,t`, repositoryID, identity.CollisionKey, identity.VersionKey), &publication)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, ErrNotFound
	}
	if err != nil {
		return CargoPublication{}, err
	}
	if publication.ObjectKey != objectKey || !publication.CollectingAt.IsZero() || !publication.CollectedAt.IsZero() {
		return CargoPublication{}, ErrDisabled
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artifact_tombstones
		WHERE repository_id::text=$1 AND format='cargo' AND coordinate=$2`, repositoryID,
		cargoPublicationCoordinate(publication.Name, publication.Version)); err != nil {
		return CargoPublication{}, err
	}
	if err := tx.Commit(); err != nil {
		return CargoPublication{}, err
	}
	return publication, nil
}

func (s *PostgresStore) ListReclaimableCargoObjects(ctx context.Context, before time.Time, limit int, after string) ([]CargoReclaimableObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT min(p.repository_id::text) AS repository_id,p.object_key,min(r.digest) AS digest,max(p.size) AS size,
		       max(t.tombstoned_at) AS tombstoned_at
		FROM native_cargo_publications p
		JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
		JOIN native_cargo_names n USING (repository_id,collision_key)
		JOIN artifact_tombstones t ON t.repository_id=p.repository_id AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version
		WHERE p.collected_at IS NULL AND p.object_key>$2
		GROUP BY p.object_key HAVING max(t.tombstoned_at)<$1
	)
	SELECT c.repository_id,c.object_key,c.digest,c.size,c.tombstoned_at FROM candidates c
	WHERE NOT EXISTS (
		SELECT 1 FROM lifecycle_jobs j WHERE j.kind='reclaim' AND j.payload->>'format'='cargo'
		  AND j.payload->>'tombstone'='true' AND j.payload->>'objectKey'=c.object_key
		  AND (j.payload->>'tombstonedAt')::timestamptz=c.tombstoned_at
	)
	ORDER BY c.object_key LIMIT $3`, before, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]CargoReclaimableObject, 0)
	for rows.Next() {
		var object CargoReclaimableObject
		if err := rows.Scan(&object.RepositoryID, &object.ObjectKey, &object.Digest, &object.Size, &object.TombstonedAt); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (s *PostgresStore) CargoObjectHasVisibleReference(ctx context.Context, objectKey string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM native_cargo_publications p
		JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
		JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.object_key=$1 AND p.collected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id
		    AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version))`, objectKey).Scan(&exists)
	return exists, err
}

func (s *PostgresStore) CargoObjectMatchesTombstone(ctx context.Context, objectKey string, expected time.Time) (bool, error) {
	var newest sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT max(t.tombstoned_at) FROM native_cargo_publications p
		JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
		JOIN native_cargo_names n USING (repository_id,collision_key)
		JOIN artifact_tombstones t ON t.repository_id=p.repository_id AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version
		WHERE p.object_key=$1 AND p.collected_at IS NULL`, objectKey).Scan(&newest)
	return newest.Valid && newest.Time.Equal(expected), err
}

func (s *PostgresStore) MarkCargoObjectCollecting(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_cargo_publications p SET collecting_at=COALESCE(p.collecting_at,now())
		FROM native_cargo_identity_reservations r,native_cargo_names n
		WHERE p.object_key=$1 AND p.collected_at IS NULL AND r.repository_id=p.repository_id AND r.collision_key=p.collision_key
		  AND r.version_key=p.version_key AND n.repository_id=p.repository_id AND n.collision_key=p.collision_key
		  AND EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id AND t.format='cargo'
		    AND t.coordinate=n.name || '@' || r.version)`, objectKey)
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

func (s *PostgresStore) MarkCargoObjectCollected(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_cargo_publications p SET collecting_at=NULL,collected_at=now()
		FROM native_cargo_identity_reservations r,native_cargo_names n
		WHERE p.object_key=$1 AND p.collected_at IS NULL AND r.repository_id=p.repository_id AND r.collision_key=p.collision_key
		  AND r.version_key=p.version_key AND n.repository_id=p.repository_id AND n.collision_key=p.collision_key
		  AND EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id AND t.format='cargo'
		    AND t.coordinate=n.name || '@' || r.version)`, objectKey)
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
