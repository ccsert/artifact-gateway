package repository

import (
	"context"
)

func (s *PostgresStore) LockArtifactScanIdentity(ctx context.Context, repositoryID string, format Format, coordinate, digest string) (func(), error) {
	return s.LockArtifactDistributionIdentities(ctx, []ArtifactDistributionLockIdentity{{RepositoryID: repositoryID, Format: format, Coordinate: coordinate, Digest: digest}})
}

func (s *PostgresStore) LockArtifactDistributionIdentities(ctx context.Context, identities []ArtifactDistributionLockIdentity) (func(), error) {
	_, release, err := s.LockArtifactDistributionIdentitiesContext(ctx, identities)
	return release, err
}

func (s *PostgresStore) LockArtifactDistributionIdentitiesContext(ctx context.Context, identities []ArtifactDistributionLockIdentity) (context.Context, func(), error) {
	keys := make([]string, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		key := artifactScanLockKey(identity.RepositoryID, identity.Format, identity.Coordinate, identity.Digest)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return s.lockPostgresAdvisoryKeys(ctx, keys)
}
