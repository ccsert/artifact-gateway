package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const artifactIntelligenceColumns = `repository_id::text,format,coordinate,digest,signatures,sboms,provenance,licenses,vulnerability,version::text,created_at,updated_at,updated_by`

func scanArtifactIntelligence(scanner interface{ Scan(...any) error }) (ArtifactIntelligence, error) {
	var value ArtifactIntelligence
	var signatures, sboms, provenance, licenses, vulnerability []byte
	if err := scanner.Scan(&value.RepositoryID, &value.Format, &value.Coordinate, &value.Digest, &signatures, &sboms, &provenance, &licenses, &vulnerability, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.UpdatedBy); err != nil {
		return ArtifactIntelligence{}, err
	}
	if err := json.Unmarshal(signatures, &value.Signatures); err != nil {
		return ArtifactIntelligence{}, err
	}
	if err := json.Unmarshal(sboms, &value.SBOMs); err != nil {
		return ArtifactIntelligence{}, err
	}
	if len(provenance) > 0 && string(provenance) != "null" {
		value.Provenance = &ArtifactProvenance{}
		if err := json.Unmarshal(provenance, value.Provenance); err != nil {
			return ArtifactIntelligence{}, err
		}
	}
	if err := json.Unmarshal(licenses, &value.Licenses); err != nil {
		return ArtifactIntelligence{}, err
	}
	if len(vulnerability) > 0 && string(vulnerability) != "null" {
		value.Vulnerability = &ArtifactVulnerabilitySummary{}
		if err := json.Unmarshal(vulnerability, value.Vulnerability); err != nil {
			return ArtifactIntelligence{}, err
		}
	}
	return value, nil
}

func marshalArtifactIntelligence(value ArtifactIntelligence) (any, any, any, any, any, error) {
	// Keep collection fields as JSON arrays even when callers omit them in a
	// repository-level write. The API contract uses [] rather than null.
	if value.Signatures == nil {
		value.Signatures = []ArtifactSignature{}
	}
	if value.SBOMs == nil {
		value.SBOMs = []ArtifactSBOM{}
	}
	if value.Licenses == nil {
		value.Licenses = []ArtifactLicense{}
	}
	signatures, err := json.Marshal(value.Signatures)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	sboms, err := json.Marshal(value.SBOMs)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	provenance, err := json.Marshal(value.Provenance)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	licenses, err := json.Marshal(value.Licenses)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	vulnerability, err := json.Marshal(value.Vulnerability)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return string(signatures), string(sboms), string(provenance), string(licenses), string(vulnerability), nil
}

func (s *PostgresStore) GetArtifactIntelligence(ctx context.Context, repositoryID string, format Format, coordinate, digest string) (ArtifactIntelligence, error) {
	value, err := scanArtifactIntelligence(s.db.QueryRowContext(ctx, `SELECT `+artifactIntelligenceColumns+` FROM artifact_intelligence WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3 AND digest=$4`, repositoryID, format, coordinate, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactIntelligence{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) ReplaceArtifactIntelligence(ctx context.Context, value ArtifactIntelligence, expectedVersion string) (ArtifactIntelligence, error) {
	signatures, sboms, provenance, licenses, vulnerability, err := marshalArtifactIntelligence(value)
	if err != nil {
		return ArtifactIntelligence{}, err
	}
	query := `INSERT INTO artifact_intelligence (repository_id,format,coordinate,digest,signatures,sboms,provenance,licenses,vulnerability,version,updated_by)
VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::jsonb,$8::jsonb,$9::jsonb,1,$10)
ON CONFLICT (repository_id,format,coordinate,digest) DO UPDATE SET signatures=$5::jsonb,sboms=$6::jsonb,provenance=$7::jsonb,licenses=$8::jsonb,vulnerability=$9::jsonb,version=artifact_intelligence.version+1,updated_by=$10,updated_at=now()
WHERE artifact_intelligence.version::text=$11
RETURNING ` + artifactIntelligenceColumns
	if expectedVersion == "" {
		expectedVersion = "0"
	}
	result, err := scanArtifactIntelligence(s.db.QueryRowContext(ctx, query, value.RepositoryID, value.Format, value.Coordinate, value.Digest, signatures, sboms, provenance, licenses, vulnerability, value.UpdatedBy, expectedVersion))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_intelligence WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3 AND digest=$4)`, value.RepositoryID, value.Format, value.Coordinate, value.Digest).Scan(&exists); queryErr != nil {
			return ArtifactIntelligence{}, queryErr
		}
		if exists {
			return ArtifactIntelligence{}, ErrVersionConflict
		}
		return ArtifactIntelligence{}, ErrNotFound
	}
	return result, err
}
