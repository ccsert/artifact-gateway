package repository

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryArtifactIdentitiesDeduplicateCanonicalPyPIPairs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "pypi", Format: FormatPyPI}); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for index, filename := range []string{"widget-1.0-py3-none-any.whl", "widget-1.0.tar.gz"} {
		if _, err := store.PublishPyPIFile(ctx, PyPIFile{
			RepositoryID: "repo", Project: "widget", Version: "1.0", Filename: filename,
			Digest: digest, ObjectKey: "pypi/" + filename, Size: 42, CreatedAt: time.Date(2026, 8, 10+index, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}

	identities, err := store.ListArtifactIdentities(ctx, "repo", FormatPyPI, ArtifactIdentityDistribution, digest, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].Coordinate != "widget@1.0" || identities[0].Digest != digest || identities[0].PublishedAt.Day() != 11 {
		t.Fatalf("identities=%#v", identities)
	}
}

func TestArtifactIdentityPurposeRejectsUnsupportedValues(t *testing.T) {
	_, err := NewMemoryStore().ListArtifactIdentities(context.Background(), "repo", FormatRaw, "browse", "", 50)
	if err == nil {
		t.Fatal("expected unsupported purpose error")
	}
}
