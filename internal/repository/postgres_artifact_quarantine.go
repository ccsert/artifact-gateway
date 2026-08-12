package repository

import (
	"context"
	"database/sql"
	"errors"
)

const artifactQuarantineColumns = `repository_id::text,format,coordinate,digest,state,reason,updated_by,version::text,quarantined_at,released_at,updated_at`

func scanArtifactQuarantine(scanner interface{ Scan(...any) error }) (ArtifactQuarantine, error) {
	var value ArtifactQuarantine
	var releasedAt sql.NullTime
	if err := scanner.Scan(&value.RepositoryID, &value.Format, &value.Coordinate, &value.Digest, &value.State, &value.Reason, &value.UpdatedBy, &value.Version, &value.QuarantinedAt, &releasedAt, &value.UpdatedAt); err != nil {
		return ArtifactQuarantine{}, err
	}
	if releasedAt.Valid {
		value.ReleasedAt = releasedAt.Time
	}
	return value, nil
}

func (s *PostgresStore) GetArtifactQuarantine(ctx context.Context, repositoryID string, format Format, coordinate, digest string) (ArtifactQuarantine, error) {
	value, err := scanArtifactQuarantine(s.db.QueryRowContext(ctx, `SELECT `+artifactQuarantineColumns+` FROM artifact_quarantines WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3 AND digest=$4`, repositoryID, format, coordinate, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactQuarantine{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) ReplaceArtifactQuarantine(ctx context.Context, value ArtifactQuarantine, expectedVersion string) (ArtifactQuarantine, error) {
	if !validArtifactQuarantine(value) {
		return ArtifactQuarantine{}, ErrInvalidArtifactQuarantine
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArtifactQuarantine{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if expectedVersion == "0" {
		if value.State != ArtifactQuarantineStateQuarantined {
			return ArtifactQuarantine{}, ErrInvalidArtifactQuarantine
		}
		created, err := scanArtifactQuarantine(tx.QueryRowContext(ctx, `INSERT INTO artifact_quarantines (repository_id,format,coordinate,digest,state,reason,updated_by,version,quarantined_at,released_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,1,now(),NULL)
RETURNING `+artifactQuarantineColumns, value.RepositoryID, value.Format, value.Coordinate, value.Digest, value.State, value.Reason, value.UpdatedBy))
		if isUnique(err) {
			return ArtifactQuarantine{}, ErrVersionConflict
		}
		if err != nil {
			return ArtifactQuarantine{}, err
		}
		if err = enqueueWebhookEventPostgres(ctx, tx, artifactQuarantineWebhookEvent(created)); err != nil {
			return ArtifactQuarantine{}, err
		}
		if err = tx.Commit(); err != nil {
			return ArtifactQuarantine{}, err
		}
		return created, nil
	}
	updated, err := scanArtifactQuarantine(tx.QueryRowContext(ctx, `UPDATE artifact_quarantines
SET state=$6,reason=$7,updated_by=$8,version=version+1,updated_at=now(),
    quarantined_at=CASE WHEN $6='quarantined' AND state<>'quarantined' THEN now() ELSE quarantined_at END,
    released_at=CASE WHEN $6='released' THEN now() ELSE NULL END
WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3 AND digest=$4 AND version::text=$5
RETURNING `+artifactQuarantineColumns, value.RepositoryID, value.Format, value.Coordinate, value.Digest, expectedVersion, value.State, value.Reason, value.UpdatedBy))
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return ArtifactQuarantine{}, err
		}
		if err = enqueueWebhookEventPostgres(ctx, tx, artifactQuarantineWebhookEvent(updated)); err != nil {
			return ArtifactQuarantine{}, err
		}
		if err = tx.Commit(); err != nil {
			return ArtifactQuarantine{}, err
		}
		return updated, nil
	}
	var exists bool
	if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_quarantines WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3 AND digest=$4)`, value.RepositoryID, value.Format, value.Coordinate, value.Digest).Scan(&exists); queryErr != nil {
		return ArtifactQuarantine{}, queryErr
	}
	if exists {
		return ArtifactQuarantine{}, ErrVersionConflict
	}
	return ArtifactQuarantine{}, ErrNotFound
}
