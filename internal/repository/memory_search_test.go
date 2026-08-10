package repository

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryArtifactSearchProjectionFindsOlderOCIManifestByDigest(t *testing.T) {
	store := NewMemoryStore()
	olderDigest := "sha256:" + strings.Repeat("a", 64)
	newerDigest := "sha256:" + strings.Repeat("b", 64)
	older := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	store.ociManifests[ociManifestKey("repo", "library/postgres", olderDigest)] = OCIManifest{
		RepositoryID: "repo", Name: "library/postgres", Digest: olderDigest, CreatedAt: older, Size: 10,
	}
	store.ociManifests[ociManifestKey("repo", "library/postgres", newerDigest)] = OCIManifest{
		RepositoryID: "repo", Name: "library/postgres", Digest: newerDigest, CreatedAt: newer, Size: 20,
	}
	if _, err := store.ReplaceArtifactIntelligence(context.Background(), ArtifactIntelligence{
		RepositoryID: "repo", Format: FormatOCI, Coordinate: "library/postgres", Digest: newerDigest,
		Signatures: []ArtifactSignature{{KeyID: "cosign-prod"}}, SBOMs: []ArtifactSBOM{{Digest: "sha256:" + strings.Repeat("c", 64)}},
		Licenses: []ArtifactLicense{{SPDXID: "Apache-2.0"}}, Vulnerability: &ArtifactVulnerabilitySummary{Status: "affected", Critical: 1, High: 2},
	}, ""); err != nil {
		t.Fatal(err)
	}

	coordinate, err := store.SearchArtifactProjection(context.Background(), "repo", FormatOCI, ArtifactSearchQuery{
		Mode: ArtifactSearchByCoordinate, Value: "library/",
	}, 10, ArtifactSearchPosition{})
	if err != nil || len(coordinate) != 1 || coordinate[0].Digest != newerDigest || coordinate[0].Intelligence == nil || coordinate[0].Intelligence.SignatureCount != 1 || coordinate[0].Intelligence.VulnerabilityStatus != "affected" || coordinate[0].Intelligence.Critical != 1 {
		t.Fatalf("coordinate=%#v err=%v", coordinate, err)
	}

	digest, err := store.SearchArtifactProjection(context.Background(), "repo", FormatOCI, ArtifactSearchQuery{
		Mode: ArtifactSearchByDigest, Value: olderDigest,
	}, 10, ArtifactSearchPosition{})
	if err != nil || len(digest) != 1 || digest[0].Coordinate != "library/postgres" || digest[0].Digest != olderDigest {
		t.Fatalf("digest=%#v err=%v", digest, err)
	}
}

func TestMemoryArtifactSearchProjectionPaginatesSnapshotBuildsWithSharedDigest(t *testing.T) {
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("c", 64)
	for build := 1; build <= 2; build++ {
		store.mavenArtifacts[string(rune('0'+build))] = MavenArtifact{
			ID: string(rune('0' + build)), RepositoryID: "repo", Coordinate: "org.example:demo:1.0-SNAPSHOT",
			Digest: digest, State: "visible", BuildNumber: build, CreatedAt: time.Now().UTC(),
		}
	}
	query := ArtifactSearchQuery{Mode: ArtifactSearchByDigest, Value: digest}
	first, err := store.SearchArtifactProjection(context.Background(), "repo", FormatMaven, query, 1, ArtifactSearchPosition{})
	if err != nil || len(first) != 1 || first[0].BuildNumber != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := store.SearchArtifactProjection(context.Background(), "repo", FormatMaven, query, 1, ArtifactSearchPosition{
		Coordinate: first[0].Coordinate, BuildNumber: first[0].BuildNumber, Digest: first[0].Digest,
	})
	if err != nil || len(second) != 1 || second[0].BuildNumber != 2 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
