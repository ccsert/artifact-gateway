package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type artifactScanCandidateIdentity struct {
	coordinate string
	digest     string
}

func (s *MemoryStore) ListArtifactScanCandidates(_ context.Context, repositoryID string, format Format, limit int) ([]ArtifactScanCandidate, error) {
	limit = artifactScanCandidateLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !formatSupportsPublicationScanReconciliation(format) {
		return nil, fmt.Errorf("format %q does not support publication scan reconciliation", format)
	}
	identities, err := s.artifactIdentitiesLocked(repositoryID, format, ArtifactIdentityScan, "")
	if err != nil {
		return nil, err
	}
	candidates := make([]ArtifactScanCandidate, 0, len(identities))
	for _, identity := range identities {
		candidates = append(candidates, ArtifactScanCandidate{
			Coordinate:  identity.Coordinate,
			Digest:      identity.Digest,
			PublishedAt: identity.PublishedAt,
		})
	}
	latestJobs := make(map[artifactScanCandidateIdentity]LifecycleJob)
	for _, job := range s.lifecycleJobs {
		if job.RepositoryID != repositoryID || job.Kind != LifecycleJobScan {
			continue
		}
		var payload ArtifactScanPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != format {
			continue
		}
		identity := artifactScanCandidateIdentity{coordinate: payload.Coordinate, digest: payload.Digest}
		latest, exists := latestJobs[identity]
		if !exists || job.CreatedAt.After(latest.CreatedAt) || job.CreatedAt.Equal(latest.CreatedAt) && job.ID > latest.ID {
			latestJobs[identity] = job
		}
	}
	priority := func(candidate ArtifactScanCandidate) int {
		job, exists := latestJobs[artifactScanCandidateIdentity{coordinate: candidate.Coordinate, digest: candidate.Digest}]
		if !exists || job.State == LifecycleJobFailed || job.State == LifecycleJobCancelled {
			return 0
		}
		return 1
	}
	return sortAndLimitArtifactScanCandidates(candidates, limit, priority), nil
}

func formatSupportsPublicationScanReconciliation(format Format) bool {
	switch format {
	case FormatMaven, FormatOCI, FormatRaw, FormatNPM, FormatPyPI, FormatConan:
		return true
	default:
		return false
	}
}

func sortAndLimitArtifactScanCandidates(candidates []ArtifactScanCandidate, limit int, priority func(ArtifactScanCandidate) int) []ArtifactScanCandidate {
	sort.Slice(candidates, func(i, j int) bool {
		if priority(candidates[i]) != priority(candidates[j]) {
			return priority(candidates[i]) < priority(candidates[j])
		}
		if candidates[i].PublishedAt.Equal(candidates[j].PublishedAt) {
			if candidates[i].Coordinate == candidates[j].Coordinate {
				return candidates[i].Digest > candidates[j].Digest
			}
			return candidates[i].Coordinate < candidates[j].Coordinate
		}
		return candidates[i].PublishedAt.After(candidates[j].PublishedAt)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func artifactScanCandidateLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 500
	}
	return limit
}
