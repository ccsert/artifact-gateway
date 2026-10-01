package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *PostgresStore) ReserveCargoIdentity(ctx context.Context, claim CargoIdentityClaim) (CargoIdentityReservation, bool, error) {
	reservation, err := normalizeCargoIdentityClaim(claim)
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, claim.RepositoryID).Scan(&exists); err != nil {
		return CargoIdentityReservation{}, false, err
	}
	if !exists {
		return CargoIdentityReservation{}, false, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_cargo_names(repository_id,collision_key,name,normalized_name)
		VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,collision_key) DO NOTHING`,
		claim.RepositoryID, reservation.CollisionKey, claim.Name, reservation.NormalizedName)
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	var storedName string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM native_cargo_names
		WHERE repository_id::text=$1 AND collision_key=$2 FOR UPDATE`,
		claim.RepositoryID, reservation.CollisionKey).Scan(&storedName); err != nil {
		return CargoIdentityReservation{}, false, err
	}
	if storedName != claim.Name {
		return CargoIdentityReservation{}, false, ErrCargoIdentityConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO native_cargo_identity_reservations
		(repository_id,collision_key,version_key,version,digest,metadata_digest) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (repository_id,collision_key,version_key) DO NOTHING`,
		claim.RepositoryID, reservation.CollisionKey, reservation.VersionKey, claim.Version, claim.Digest, claim.MetadataDigest)
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	var storedVersion, storedDigest, storedMetadataDigest string
	if err = tx.QueryRowContext(ctx, `SELECT version,digest,metadata_digest,created_at FROM native_cargo_identity_reservations
		WHERE repository_id::text=$1 AND collision_key=$2 AND version_key=$3 FOR UPDATE`,
		claim.RepositoryID, reservation.CollisionKey, reservation.VersionKey).
		Scan(&storedVersion, &storedDigest, &storedMetadataDigest, &reservation.CreatedAt); err != nil {
		return CargoIdentityReservation{}, false, err
	}
	if storedVersion != claim.Version || storedDigest != claim.Digest || storedMetadataDigest != claim.MetadataDigest {
		return CargoIdentityReservation{}, false, ErrCargoIdentityConflict
	}
	if err = tx.Commit(); err != nil {
		return CargoIdentityReservation{}, false, err
	}
	return reservation, inserted == 0, nil
}

func (s *PostgresStore) GetCargoIdentityReservation(ctx context.Context, repositoryID, name, version string) (CargoIdentityReservation, error) {
	normalized, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	item := CargoIdentityReservation{
		CargoIdentityClaim: CargoIdentityClaim{RepositoryID: repositoryID},
		CollisionKey:       normalized.CollisionKey,
		VersionKey:         normalized.VersionKey,
	}
	err = s.db.QueryRowContext(ctx, `SELECT n.name,n.normalized_name,v.version,v.digest,v.metadata_digest,v.created_at
		FROM native_cargo_identity_reservations v
		JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE v.repository_id::text=$1 AND v.collision_key=$2 AND v.version_key=$3`,
		repositoryID, normalized.CollisionKey, normalized.VersionKey).
		Scan(&item.Name, &item.NormalizedName, &item.Version, &item.Digest, &item.MetadataDigest, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoIdentityReservation{}, ErrNotFound
	}
	if err != nil {
		return CargoIdentityReservation{}, err
	}
	return item, nil
}
