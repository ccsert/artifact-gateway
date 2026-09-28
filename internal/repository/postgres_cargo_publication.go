package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

const cargoPublicationColumns = `p.repository_id::text,n.name,r.version,r.digest,r.metadata_digest,
	p.object_key,p.size,p.index_row,p.description,p.publisher,p.published_at,p.created_at,p.yanked,p.updated_at`

func (s *PostgresStore) CommitCargoPublication(ctx context.Context, incoming CargoPublication) (CargoPublication, bool, error) {
	publication, normalized, err := normalizeCargoPublication(incoming)
	if err != nil {
		return CargoPublication{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CargoPublication{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var format Format
	var repoType RepositoryType
	var state RepositoryState
	err = tx.QueryRowContext(ctx, `SELECT format,repo_type,state FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, publication.RepositoryID).
		Scan(&format, &repoType, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (format != FormatCargo || repoType != RepositoryTypeHosted || state != RepositoryActive)) {
		return CargoPublication{}, false, ErrNotFound
	}
	if err != nil {
		return CargoPublication{}, false, err
	}
	var reservation CargoIdentityReservation
	err = tx.QueryRowContext(ctx, `SELECT n.name,r.version,r.digest,r.metadata_digest,r.created_at
		FROM native_cargo_identity_reservations r
		JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE r.repository_id::text=$1 AND r.collision_key=$2 AND r.version_key=$3 FOR UPDATE OF r`,
		publication.RepositoryID, normalized.CollisionKey, normalized.VersionKey).
		Scan(&reservation.Name, &reservation.Version, &reservation.Digest, &reservation.MetadataDigest, &reservation.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, false, ErrCargoIdentityConflict
	}
	if err != nil {
		return CargoPublication{}, false, err
	}
	if !cargoReservationMatches(reservation, publication) {
		return CargoPublication{}, false, ErrCargoIdentityConflict
	}
	var existing CargoPublication
	err = scanCargoPublication(tx.QueryRowContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key=$3 FOR UPDATE OF p`,
		publication.RepositoryID, normalized.CollisionKey, normalized.VersionKey), &existing)
	if err == nil {
		if !cargoPublicationMatches(existing, publication) {
			return CargoPublication{}, false, ErrCargoPublicationConflict
		}
		if err = tx.Commit(); err != nil {
			return CargoPublication{}, false, err
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, false, err
	}
	// Serialize publication with Group reconciliation and reject a collision
	// before the Hosted version becomes visible.
	groupRows, err := tx.QueryContext(ctx, `SELECT g.id::text FROM hosted_groups g
		JOIN hosted_group_members m ON m.group_id=g.id
		WHERE m.repository_id::text=$1 AND g.format='cargo' ORDER BY g.id FOR UPDATE OF g`, publication.RepositoryID)
	if err != nil {
		return CargoPublication{}, false, err
	}
	groupIDs := make([]string, 0)
	for groupRows.Next() {
		var groupID string
		if err := groupRows.Scan(&groupID); err != nil {
			_ = groupRows.Close()
			return CargoPublication{}, false, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	err = groupRows.Err()
	_ = groupRows.Close()
	if err != nil {
		return CargoPublication{}, false, err
	}
	for _, groupID := range groupIDs {
		var owner CargoGroupVersion
		err := tx.QueryRowContext(ctx, `SELECT group_id::text,source_repository_id::text,name,version,checksum,index_row
			FROM native_cargo_group_versions WHERE group_id::text=$1 AND collision_key=$2 AND version_key=$3`,
			groupID, normalized.CollisionKey, normalized.VersionKey).
			Scan(&owner.GroupID, &owner.SourceRepositoryID, &owner.Name, &owner.Version, &owner.Checksum, &owner.IndexRow)
		if err == nil && !cargoGroupPublicationCompatible(owner, publication) {
			return CargoPublication{}, false, ErrCargoGroupConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CargoPublication{}, false, err
		}
	}
	var quota, used int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT quota_bytes FROM repository_capacity_quotas WHERE repository_id=h.id),0),
		COALESCE((SELECT SUM(size) FROM native_cargo_publications WHERE repository_id=h.id),0)
		FROM hosted_repositories h WHERE h.id::text=$1`, publication.RepositoryID).Scan(&quota, &used)
	if err != nil {
		return CargoPublication{}, false, err
	}
	if quota > 0 && (used >= quota || publication.Size > quota-used) {
		return CargoPublication{}, false, ErrQuotaExceeded
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO native_cargo_publications
		(repository_id,collision_key,version_key,object_key,size,index_row,description,publisher,published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at,yanked,updated_at`, publication.RepositoryID,
		normalized.CollisionKey, normalized.VersionKey, publication.ObjectKey, publication.Size,
		string(publication.IndexRow), publication.Description, publication.Publisher, publication.PublishedAt).
		Scan(&publication.CreatedAt, &publication.Yanked, &publication.UpdatedAt)
	if IsQuotaExceeded(err) {
		return CargoPublication{}, false, ErrQuotaExceeded
	}
	if isUnique(err) {
		return CargoPublication{}, false, ErrCargoPublicationConflict
	}
	if err != nil {
		return CargoPublication{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return CargoPublication{}, false, err
	}
	return publication, false, nil
}

func (s *PostgresStore) GetCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoPublication{}, ErrInvalidCargoIdentity
	}
	var publication CargoPublication
	err = scanCargoPublication(s.db.QueryRowContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key=$3`,
		repositoryID, identity.CollisionKey, identity.VersionKey), &publication)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, ErrNotFound
	}
	return publication, err
}

func (s *PostgresStore) ListCargoPublications(ctx context.Context, repositoryID, name string) ([]CargoPublication, error) {
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || repositoryID == "" {
		return nil, ErrInvalidCargoIdentity
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 ORDER BY p.version_key`, repositoryID, identity.CollisionKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]CargoPublication, 0)
	for rows.Next() {
		var publication CargoPublication
		if err = scanCargoPublication(rows, &publication); err != nil {
			return nil, err
		}
		items = append(items, publication)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
}

func (s *PostgresStore) SetCargoYanked(ctx context.Context, repositoryID, name, version string, yanked bool) (CargoPublication, bool, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoPublication{}, false, ErrInvalidCargoIdentity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CargoPublication{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var format Format
	var repoType RepositoryType
	var state RepositoryState
	err = tx.QueryRowContext(ctx, `SELECT format,repo_type,state FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, repositoryID).
		Scan(&format, &repoType, &state)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (format != FormatCargo || repoType != RepositoryTypeHosted || state != RepositoryActive) {
		return CargoPublication{}, false, ErrNotFound
	}
	if err != nil {
		return CargoPublication{}, false, err
	}
	var publication CargoPublication
	err = scanCargoPublication(tx.QueryRowContext(ctx, `SELECT `+cargoPublicationColumns+`
		FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
		USING (repository_id,collision_key,version_key) JOIN native_cargo_names n USING (repository_id,collision_key)
		WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key=$3 FOR UPDATE OF p`,
		repositoryID, identity.CollisionKey, identity.VersionKey), &publication)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoPublication{}, false, ErrNotFound
	}
	if err != nil {
		return CargoPublication{}, false, err
	}
	changed := publication.Yanked != yanked
	if changed {
		err = tx.QueryRowContext(ctx, `UPDATE native_cargo_publications SET yanked=$4,updated_at=now()
			WHERE repository_id::text=$1 AND collision_key=$2 AND version_key=$3 RETURNING yanked,updated_at`,
			repositoryID, identity.CollisionKey, identity.VersionKey, yanked).Scan(&publication.Yanked, &publication.UpdatedAt)
		if err != nil {
			return CargoPublication{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return CargoPublication{}, false, err
	}
	return publication, changed, nil
}

func scanCargoPublication(row interface{ Scan(...any) error }, publication *CargoPublication) error {
	return row.Scan(&publication.RepositoryID, &publication.Name, &publication.Version, &publication.Digest,
		&publication.MetadataDigest, &publication.ObjectKey, &publication.Size, &publication.IndexRow, &publication.Description,
		&publication.Publisher, &publication.PublishedAt, &publication.CreatedAt, &publication.Yanked, &publication.UpdatedAt)
}

func (s *PostgresStore) LockCargoObject(ctx context.Context, objectKey string) (func(), error) {
	_, release, err := s.LockArtifactObjectKeys(ctx, FormatCargo, []string{objectKey})
	return release, err
}

func (s *PostgresStore) CargoObjectHasReference(ctx context.Context, objectKey string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_cargo_publications WHERE object_key=$1)`, objectKey).Scan(&exists)
	return exists, err
}
