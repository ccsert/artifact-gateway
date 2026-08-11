package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMemoryArtifactQuarantineUsesOptimisticVersion(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	identity := ArtifactQuarantine{
		RepositoryID: "repo",
		Format:       FormatRaw,
		Coordinate:   "releases/widget.bin",
		Digest:       "sha256:" + strings.Repeat("a", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "critical vulnerability under investigation",
		UpdatedBy:    "user:alice",
	}
	if _, err := store.GetArtifactQuarantine(ctx, identity.RepositoryID, identity.Format, identity.Coordinate, identity.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err=%v", err)
	}
	created, err := store.ReplaceArtifactQuarantine(ctx, identity, "0")
	if err != nil || created.Version != "1" || created.QuarantinedAt.IsZero() || !created.ReleasedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, identity, "0"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale create err=%v", err)
	}

	release := identity
	release.State = ArtifactQuarantineStateReleased
	release.Reason = "finding accepted after review"
	release.UpdatedBy = "user:bob"
	released, err := store.ReplaceArtifactQuarantine(ctx, release, created.Version)
	if err != nil || released.Version != "2" || released.ReleasedAt.IsZero() || !released.QuarantinedAt.Equal(created.QuarantinedAt) {
		t.Fatalf("released=%#v err=%v", released, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, release, created.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale release err=%v", err)
	}

	requarantine := identity
	requarantine.Reason = "new critical finding"
	requarantine.UpdatedBy = "scanner:grype"
	quarantined, err := store.ReplaceArtifactQuarantine(ctx, requarantine, released.Version)
	if err != nil || quarantined.Version != "3" || quarantined.QuarantinedAt.IsZero() || !quarantined.ReleasedAt.IsZero() {
		t.Fatalf("requarantined=%#v err=%v", quarantined, err)
	}
	loaded, err := store.GetArtifactQuarantine(ctx, identity.RepositoryID, identity.Format, identity.Coordinate, identity.Digest)
	if err != nil || loaded != quarantined {
		t.Fatalf("loaded=%#v want=%#v err=%v", loaded, quarantined, err)
	}
}

func TestMemoryArtifactQuarantineConcurrentCASHasOneWinner(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	value := ArtifactQuarantine{
		RepositoryID: "repo",
		Format:       FormatOCI,
		Coordinate:   "library/widget",
		Digest:       "sha256:" + strings.Repeat("b", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "investigating",
		UpdatedBy:    "user:alice",
	}
	created, err := store.ReplaceArtifactQuarantine(ctx, value, "0")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, actor := range []string{"user:bob", "user:carol"} {
		actor := actor
		go func() {
			release := value
			release.State = ArtifactQuarantineStateReleased
			release.Reason = "reviewed by " + actor
			release.UpdatedBy = actor
			_, replaceErr := store.ReplaceArtifactQuarantine(ctx, release, created.Version)
			results <- replaceErr
		}()
	}
	var succeeded, conflicted int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("replace err=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestMemoryArtifactQuarantineRejectsReleasedInitialState(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.ReplaceArtifactQuarantine(context.Background(), ArtifactQuarantine{
		RepositoryID: "repo",
		Format:       FormatRaw,
		Coordinate:   "releases/widget.bin",
		Digest:       "sha256:" + strings.Repeat("c", 64),
		State:        ArtifactQuarantineStateReleased,
		Reason:       "not quarantined",
		UpdatedBy:    "user:alice",
	}, "0")
	if !errors.Is(err, ErrInvalidArtifactQuarantine) {
		t.Fatalf("err=%v", err)
	}
}
