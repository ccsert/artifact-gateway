package repository

import (
	"context"
	"database/sql"
	"strings"
)

// APTArtifactStore resolves only packages in the current signed publication.
// Governance/scanning deliberately bypass the optional protocol read policy.
// Retired pool objects remain download candidates, never new scan identities.
type APTArtifactStore interface {
	GetAPTScanAsset(context.Context, string, string, string) (APTSnapshotAsset, error)
}

// ValidAPTArtifactCoordinate is the repository-global immutable pool URL. A
// suite-qualified identity would let another suite bypass the same quarantine.
func ValidAPTArtifactCoordinate(coordinate string) bool {
	parts := strings.Split(coordinate, "/")
	return len(parts) == 5 && parts[0] == "pool" && ValidAPTPublicationScope(parts[1]) &&
		ValidAPTRepositoryPath(coordinate) && ValidAPTObjectName(parts[4]) &&
		parts[3] != "" && APTPoolPath(parts[1], parts[3], parts[4]) == coordinate
}

func (s *MemoryStore) GetAPTScanAsset(_ context.Context, repoID, coordinate, digest string) (APTSnapshotAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !ValidAPTArtifactCoordinate(coordinate) {
		return APTSnapshotAsset{}, ErrNotFound
	}
	for id, assets := range s.aptSnapshotAssets {
		snapshot := s.aptSnapshots[id]
		if snapshot.RepositoryID != repoID || snapshot.State != APTRepositorySnapshotVisible {
			continue
		}
		for _, a := range assets {
			if a.Path == coordinate && a.Digest == digest && a.ContentType == "application/vnd.debian.binary-package" {
				return a, nil
			}
		}
	}
	return APTSnapshotAsset{}, ErrNotFound
}

func (s *PostgresStore) GetAPTScanAsset(ctx context.Context, repoID, coordinate, digest string) (APTSnapshotAsset, error) {
	if !ValidAPTArtifactCoordinate(coordinate) {
		return APTSnapshotAsset{}, ErrNotFound
	}
	var asset APTSnapshotAsset
	err := scanAPTSnapshotAsset(s.db.QueryRowContext(ctx, `SELECT `+aptSnapshotAssetColumns+`
 FROM native_apt_snapshot_assets a JOIN native_apt_repository_snapshots s ON s.id=a.snapshot_id
 WHERE a.repository_id::text=$1 AND a.path=$2 AND a.digest=$3 AND s.state='visible'
 AND a.content_type='application/vnd.debian.binary-package' ORDER BY s.published_at DESC,s.id LIMIT 1`, repoID, coordinate, digest), &asset)
	if err == sql.ErrNoRows {
		return APTSnapshotAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *MemoryStore) aptAssetsQuarantinedLocked(repoID string, assets []APTSnapshotAsset, path string) bool {
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Path, "pool/") || (strings.HasPrefix(path, "pool/") && asset.Path != path) {
			continue
		}
		value := s.artifactQuarantines[artifactQuarantineKey(repoID, FormatAPT, asset.Path, asset.Digest)]
		if value.State == ArtifactQuarantineStateQuarantined {
			return true
		}
	}
	return false
}

// The caller holds the Hosted repository row lock, also held by APT quarantine
// transitions. The final visibility transaction is therefore the admission fence.
func checkAPTAssetsAdmissionTx(ctx context.Context, tx *sql.Tx, repoID string, assets []APTSnapshotAsset) error {
	paths := make([]string, 0)
	digests := make(map[string]string)
	for _, asset := range assets {
		if strings.HasPrefix(asset.Path, "pool/") {
			paths = append(paths, asset.Path)
			digests[asset.Path] = asset.Digest
		}
	}
	if len(paths) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT coordinate,digest FROM artifact_quarantines WHERE repository_id=$1 AND format='apt' AND state='quarantined' AND coordinate=ANY($2)`, repoID, paths)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var path, digest string
		if err = rows.Scan(&path, &digest); err != nil {
			return err
		}
		if digests[path] == digest {
			return ErrArtifactQuarantined
		}
	}
	return rows.Err()
}
