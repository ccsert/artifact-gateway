package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type artifactScanCandidateIdentity struct {
	coordinate string
	digest     string
}

func (s *MemoryStore) ListArtifactScanCandidates(_ context.Context, repositoryID string, format Format, limit int) ([]ArtifactScanCandidate, error) {
	limit = artifactScanCandidateLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := make([]ArtifactScanCandidate, 0, limit)
	appendCandidate := func(coordinate, digest string, publishedAt time.Time) {
		if coordinate != "" && digest != "" {
			candidates = append(candidates, ArtifactScanCandidate{Coordinate: coordinate, Digest: digest, PublishedAt: publishedAt})
		}
	}
	switch format {
	case FormatMaven:
		for _, artifact := range s.mavenArtifacts {
			if artifact.RepositoryID == repositoryID && artifact.State == "visible" {
				appendCandidate(artifact.Coordinate, artifact.Digest, artifact.CreatedAt)
			}
		}
	case FormatOCI:
		for _, manifest := range s.ociManifests {
			if manifest.RepositoryID == repositoryID {
				appendCandidate(manifest.Name, manifest.Digest, manifest.CreatedAt)
			}
		}
	case FormatRaw:
		for _, asset := range s.rawAssets {
			if asset.RepositoryID == repositoryID {
				appendCandidate(asset.Path, asset.Digest, asset.UpdatedAt)
			}
		}
	case FormatNPM:
		for _, version := range s.npmVersions {
			if version.RepositoryID == repositoryID && version.State == "visible" {
				appendCandidate(version.PackageName+"@"+version.Version, version.Digest, version.CreatedAt)
			}
		}
	case FormatPyPI:
		for _, file := range s.pypiFiles {
			if file.RepositoryID == repositoryID && file.State == "visible" {
				appendCandidate(file.Project+"@"+file.Version, file.Digest, file.CreatedAt)
			}
		}
	case FormatConan:
		for _, revision := range s.conanRecipes {
			if revision.RepositoryID == repositoryID && revision.State == "visible" {
				appendCandidate(revision.Reference+"#"+revision.Revision, revision.Digest, revision.CreatedAt)
			}
		}
		for _, revision := range s.conanPackages {
			recipe, recipeExists := s.conanRecipes[conanRecipeKey(repositoryID, revision.Reference, revision.RecipeRevision)]
			if revision.RepositoryID == repositoryID && revision.State == "visible" && recipeExists && recipe.State == "visible" {
				appendCandidate(revision.Reference+"#"+revision.RecipeRevision+"/"+revision.PackageID+"#"+revision.Revision, revision.Digest, revision.CreatedAt)
			}
		}
	default:
		return nil, fmt.Errorf("format %q does not support publication scan reconciliation", format)
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
