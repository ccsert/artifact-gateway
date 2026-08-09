package repository

import (
	"context"
	"testing"
)

func TestMemoryArtifactIntelligenceReplaceUsesOptimisticVersion(t *testing.T) {
	store := NewMemoryStore()
	value := ArtifactIntelligence{RepositoryID: "repo", Format: FormatOCI, Coordinate: "library/widget", Digest: "sha256:" + repeatHex("a"), Licenses: []ArtifactLicense{{SPDXID: "MIT", Name: "MIT License"}}}
	created, err := store.ReplaceArtifactIntelligence(context.Background(), value, "")
	if err != nil || created.Version != "1" || created.CreatedAt.IsZero() {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	if created.Signatures == nil || created.SBOMs == nil || created.Licenses == nil {
		t.Fatalf("collection fields must be empty arrays, got signatures=%#v sboms=%#v licenses=%#v", created.Signatures, created.SBOMs, created.Licenses)
	}
	value.Licenses = []ArtifactLicense{{SPDXID: "Apache-2.0", Name: "Apache License 2.0"}}
	updated, err := store.ReplaceArtifactIntelligence(context.Background(), value, "1")
	if err != nil || updated.Version != "2" || updated.Licenses[0].SPDXID != "Apache-2.0" {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, err = store.ReplaceArtifactIntelligence(context.Background(), value, "1"); err != ErrVersionConflict {
		t.Fatalf("stale err=%v", err)
	}
}

func repeatHex(value string) string {
	return value + value + value + value + value + value + value + value
}
