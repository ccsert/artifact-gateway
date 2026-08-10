package repository

import (
	"context"
	"errors"
	"testing"
)

type failingArtifactIntelligenceQueue struct {
	*MemoryStore
	err error
}

func (s *failingArtifactIntelligenceQueue) EnqueueLifecycleJob(context.Context, LifecycleJob) (LifecycleJob, bool, error) {
	return LifecycleJob{}, false, s.err
}

func (s *failingArtifactIntelligenceQueue) ReplaceArtifactIntelligence(ctx context.Context, value ArtifactIntelligence, expectedVersion string) (ArtifactIntelligence, error) {
	if value.RepositoryID == "target" {
		return ArtifactIntelligence{}, s.err
	}
	return s.MemoryStore.ReplaceArtifactIntelligence(ctx, value, expectedVersion)
}

func TestCopyArtifactIntelligenceIsIdempotentAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	value := ArtifactIntelligence{
		RepositoryID: "source",
		Format:       FormatOCI,
		Coordinate:   "library/widget",
		Digest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SBOMs:        []ArtifactSBOM{{MediaType: "application/spdx+json", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
	}
	if _, err := store.ReplaceArtifactIntelligence(ctx, value, ""); err != nil {
		t.Fatal(err)
	}
	if err := CopyArtifactIntelligence(ctx, store, "target", "source", value.Format, value.Coordinate, value.Digest); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	if err := CopyArtifactIntelligence(ctx, store, "target", "source", value.Format, value.Coordinate, value.Digest); err != nil {
		t.Fatalf("idempotent copy: %v", err)
	}
	target, err := store.GetArtifactIntelligence(ctx, "target", value.Format, value.Coordinate, value.Digest)
	if err != nil || len(target.SBOMs) != 1 {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	changed := value
	changed.RepositoryID = "target"
	changed.Licenses = []ArtifactLicense{{SPDXID: "MIT"}}
	if _, err = store.ReplaceArtifactIntelligence(ctx, changed, target.Version); err != nil {
		t.Fatal(err)
	}
	if err = CopyArtifactIntelligence(ctx, store, "target", "source", value.Format, value.Coordinate, value.Digest); !errors.Is(err, ErrArtifactIntelligenceConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestCopyArtifactIntelligenceOrEnqueueMarksQueueFailureAsDeferred(t *testing.T) {
	ctx := context.Background()
	base := NewMemoryStore()
	value := ArtifactIntelligence{
		RepositoryID: "source",
		Format:       FormatRaw,
		Coordinate:   "releases/widget.zip",
		Digest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Licenses:     []ArtifactLicense{{SPDXID: "Apache-2.0"}},
	}
	if _, err := base.ReplaceArtifactIntelligence(ctx, value, ""); err != nil {
		t.Fatal(err)
	}
	store := &failingArtifactIntelligenceQueue{MemoryStore: base, err: errors.New("database unavailable")}
	err := CopyArtifactIntelligenceOrEnqueue(ctx, store, store, "target", "source", value.Format, value.Coordinate, value.Digest)
	if !errors.Is(err, ErrArtifactIntelligenceDeferred) {
		t.Fatalf("error=%v, want deferred marker", err)
	}
	if _, err = base.GetArtifactIntelligence(ctx, "target", value.Format, value.Coordinate, value.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("target intelligence=%v, want no partial write", err)
	}
}
