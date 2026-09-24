package app

import (
	"context"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// usageEvidence is the lifecycle download evidence matched for one retention
// candidate from the artifact usage aggregate.
type usageEvidence struct {
	DownloadCount    int64
	LastDownloadedAt time.Time
}

func (e usageEvidence) add(other usageEvidence) usageEvidence {
	merged := usageEvidence{
		DownloadCount:    e.DownloadCount + other.DownloadCount,
		LastDownloadedAt: e.LastDownloadedAt,
	}
	if other.LastDownloadedAt.After(merged.LastDownloadedAt) {
		merged.LastDownloadedAt = other.LastDownloadedAt
	}
	return merged
}

// downloadedWithin reports whether the unit was downloaded inside the
// protection window ending at now.
func (e usageEvidence) downloadedWithin(days int, now time.Time) bool {
	if days <= 0 || e.DownloadCount == 0 || e.LastDownloadedAt.IsZero() {
		return false
	}
	return e.LastDownloadedAt.After(now.AddDate(0, 0, -days))
}

// retentionUsageIndex folds one repository's usage stats into the address
// spaces retention candidates can be matched against. npm, PyPI, Go, and
// Maven downloads record the candidate coordinate directly (Maven via
// mavenResourceFromPath); Raw records the asset path; OCI group pulls record
// the request path carrying the manifest digest; Conan v2 file downloads
// record "name/version/user/channel/revisions/<rrev>/..." paths.
type retentionUsageIndex struct {
	byCoordinate map[string]usageEvidence
	byPath       map[string]usageEvidence
	byDigest     map[string]usageEvidence
	byRevision   map[string]usageEvidence
}

func (m NativeRepositoryRetention) loadRetentionUsageIndex(ctx context.Context, repositoryName string, candidates []RepositoryRetentionCandidate) (*retentionUsageIndex, error) {
	index := &retentionUsageIndex{}
	if repositoryName == "" || len(candidates) == 0 {
		return index, nil
	}
	wantedCoordinates := make(map[string]bool)
	wantedPaths := make(map[string]bool)
	wantedDigests := make(map[string]bool)
	wantedRevisions := make(map[string]bool)
	for _, candidate := range candidates {
		switch candidate.Format {
		case repository.FormatMaven, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo:
			wantedCoordinates[candidate.Coordinate] = true
		case repository.FormatRaw:
			wantedPaths[candidate.rawPath] = true
			wantedPaths[candidate.Coordinate] = true
		case repository.FormatOCI:
			wantedDigests[candidate.Digest] = true
		case repository.FormatConan:
			wantedRevisions[conanReferenceUsagePath(candidate.conanRef)+"#"+candidate.conanRevision] = true
		}
	}
	err := m.Store.WalkArtifactUsage(ctx, repositoryName, func(stat repository.ArtifactUsageStat) error {
		evidence := usageEvidence{DownloadCount: stat.DownloadCount, LastDownloadedAt: stat.LastDownloadedAt}
		switch repository.Format(stat.Format) {
		case repository.FormatNPM, repository.FormatPyPI, repository.FormatGo:
			if wantedCoordinates[stat.Resource] {
				index.byCoordinate = addUsageEvidence(index.byCoordinate, stat.Resource, evidence)
			}
		case repository.FormatMaven:
			coordinate := mavenResourceFromPath(stat.Resource)
			if wantedCoordinates[coordinate] {
				index.byCoordinate = addUsageEvidence(index.byCoordinate, coordinate, evidence)
			}
		case repository.FormatRaw:
			if wantedPaths[stat.Resource] {
				index.byPath = addUsageEvidence(index.byPath, stat.Resource, evidence)
			}
		case repository.FormatOCI:
			if digest, ok := ociManifestDigestFromUsageResource(stat.Resource); ok && wantedDigests[digest] {
				index.byDigest = addUsageEvidence(index.byDigest, digest, evidence)
			}
		case repository.FormatConan:
			if reference, revision, ok := conanRevisionFromUsageResource(stat.Resource); ok && wantedRevisions[reference+"#"+revision] {
				index.byRevision = addUsageEvidence(index.byRevision, reference+"#"+revision, evidence)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func addUsageEvidence(index map[string]usageEvidence, key string, evidence usageEvidence) map[string]usageEvidence {
	if key == "" {
		return index
	}
	if index == nil {
		index = make(map[string]usageEvidence)
	}
	index[key] = index[key].add(evidence)
	return index
}

// match returns the usage evidence recorded for one candidate.
func (index *retentionUsageIndex) match(candidate *RepositoryRetentionCandidate) usageEvidence {
	if index == nil {
		return usageEvidence{}
	}
	switch candidate.Format {
	case repository.FormatMaven, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo:
		return index.byCoordinate[candidate.Coordinate]
	case repository.FormatRaw:
		if evidence, ok := index.byPath[candidate.rawPath]; ok {
			return evidence
		}
		return index.byPath[candidate.Coordinate]
	case repository.FormatOCI:
		return index.byDigest[candidate.Digest]
	case repository.FormatConan:
		return index.byRevision[conanReferenceUsagePath(candidate.conanRef)+"#"+candidate.conanRevision]
	default:
		return usageEvidence{}
	}
}

// ociManifestDigestFromUsageResource extracts the manifest digest from an OCI
// download path such as /v2/<name>/manifests/sha256:.... Tag pulls cannot be
// attributed to a digest and are skipped.
func ociManifestDigestFromUsageResource(resource string) (string, bool) {
	_, reference, found := strings.Cut(resource, "/manifests/")
	if !found {
		return "", false
	}
	if reference != "" && strings.HasPrefix(reference, "sha256:") && len(reference) == len("sha256:")+64 {
		return reference, true
	}
	return "", false
}

// conanRevisionFromUsageResource parses a Conan v2 download path recorded by
// the audit stream into its reference path form and recipe revision.
func conanRevisionFromUsageResource(resource string) (reference, revision string, ok bool) {
	parts := strings.Split(strings.Trim(resource, "/"), "/")
	if len(parts) < 6 || parts[4] != "revisions" {
		return "", "", false
	}
	reference = strings.Join(parts[0:4], "/")
	revision = parts[5]
	if reference == "" || revision == "" {
		return "", "", false
	}
	return reference, revision, true
}

// conanReferenceUsagePath converts a Conan reference into the path form the
// protocol records: name/version@user/channel -> name/version/user/channel.
func conanReferenceUsagePath(reference string) string {
	return strings.Replace(reference, "@", "/", 1)
}
