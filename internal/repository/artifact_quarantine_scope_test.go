package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestArtifactQuarantineAdmissionIsScopedToExactIdentity(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("a", 64)
	coordinate := "releases/widget.bin"
	created, err := store.ReplaceArtifactQuarantine(ctx, ArtifactQuarantine{
		RepositoryID: "source-a",
		Format:       FormatRaw,
		Coordinate:   coordinate,
		Digest:       digest,
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "critical vulnerability under investigation",
		UpdatedBy:    "user:alice",
	}, "0")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		repositoryID string
		format       Format
		coordinate   string
		digest       string
		allowed      bool
	}{
		{name: "exact identity", repositoryID: "source-a", format: FormatRaw, coordinate: coordinate, digest: digest, allowed: false},
		{name: "other repository", repositoryID: "source-b", format: FormatRaw, coordinate: coordinate, digest: digest, allowed: true},
		{name: "other format", repositoryID: "source-a", format: FormatAPT, coordinate: coordinate, digest: digest, allowed: true},
		{name: "other coordinate", repositoryID: "source-a", format: FormatRaw, coordinate: "releases/other.bin", digest: digest, allowed: true},
		{name: "other digest", repositoryID: "source-a", format: FormatRaw, coordinate: coordinate, digest: "sha256:" + strings.Repeat("b", 64), allowed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, admissionErr := ArtifactDistributionAllowed(ctx, store, test.repositoryID, test.format, test.coordinate, test.digest)
			if admissionErr != nil || allowed != test.allowed {
				t.Fatalf("allowed=%t want=%t err=%v", allowed, test.allowed, admissionErr)
			}
		})
	}

	released := created
	released.State = ArtifactQuarantineStateReleased
	released.Reason = "review completed"
	released.UpdatedBy = "user:bob"
	if _, err = store.ReplaceArtifactQuarantine(ctx, released, created.Version); err != nil {
		t.Fatal(err)
	}
	allowed, err := ArtifactDistributionAllowed(ctx, store, "source-a", FormatRaw, coordinate, digest)
	if err != nil || !allowed {
		t.Fatalf("released identity allowed=%t err=%v", allowed, err)
	}
}

func TestArtifactQuarantineValidationBounds(t *testing.T) {
	base := ArtifactQuarantine{
		RepositoryID: "repo",
		Format:       FormatRaw,
		Coordinate:   "releases/widget.bin",
		Digest:       "sha256:" + strings.Repeat("c", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       strings.Repeat("r", 1024),
		UpdatedBy:    "user:alice",
	}
	if _, err := NewMemoryStore().ReplaceArtifactQuarantine(context.Background(), base, "0"); err != nil {
		t.Fatalf("maximum bounded reason should be accepted: %v", err)
	}
	unicodeReason := base
	unicodeReason.Reason = strings.Repeat("界", 1024)
	if _, err := NewMemoryStore().ReplaceArtifactQuarantine(context.Background(), unicodeReason, "0"); err != nil {
		t.Fatalf("maximum Unicode reason should be accepted: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ArtifactQuarantine)
	}{
		{name: "empty reason", mutate: func(value *ArtifactQuarantine) { value.Reason = "   " }},
		{name: "long reason", mutate: func(value *ArtifactQuarantine) { value.Reason = strings.Repeat("r", 1025) }},
		{name: "long Unicode reason", mutate: func(value *ArtifactQuarantine) { value.Reason = strings.Repeat("界", 1025) }},
		{name: "uppercase digest", mutate: func(value *ArtifactQuarantine) { value.Digest = "sha256:" + strings.Repeat("A", 64) }},
		{name: "empty actor", mutate: func(value *ArtifactQuarantine) { value.UpdatedBy = "" }},
		{name: "whitespace coordinate", mutate: func(value *ArtifactQuarantine) { value.Coordinate = "   " }},
		{name: "long coordinate", mutate: func(value *ArtifactQuarantine) { value.Coordinate = strings.Repeat("c", 1025) }},
		{name: "long actor", mutate: func(value *ArtifactQuarantine) { value.UpdatedBy = strings.Repeat("a", 513) }},
		{name: "nul coordinate", mutate: func(value *ArtifactQuarantine) { value.Coordinate = "bad\x00coordinate" }},
		{name: "nul reason", mutate: func(value *ArtifactQuarantine) { value.Reason = "bad\x00reason" }},
		{name: "nul actor", mutate: func(value *ArtifactQuarantine) { value.UpdatedBy = "bad\x00actor" }},
		{name: "Conan package revision is not a distribution anchor", mutate: func(value *ArtifactQuarantine) {
			value.Format = FormatConan
			value.Coordinate = "widget/1.0/team/stable#rrev/package-id#prev"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, err := NewMemoryStore().ReplaceArtifactQuarantine(context.Background(), value, "0"); !errors.Is(err, ErrInvalidArtifactQuarantine) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestArtifactQuarantineAcceptsConanRecipeRevisionAnchor(t *testing.T) {
	value := ArtifactQuarantine{
		RepositoryID: "repo",
		Format:       FormatConan,
		Coordinate:   "widget/1.0/team/stable#rrev",
		Digest:       "sha256:" + strings.Repeat("d", 64),
		State:        ArtifactQuarantineStateQuarantined,
		Reason:       "recipe revision under investigation",
		UpdatedBy:    "user:alice",
	}
	if _, err := NewMemoryStore().ReplaceArtifactQuarantine(context.Background(), value, "0"); err != nil {
		t.Fatalf("valid Conan recipe quarantine anchor rejected: %v", err)
	}
}
