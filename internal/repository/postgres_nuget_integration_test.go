//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newPostgresNuGetStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestPostgresNuGetStoreContract(t *testing.T) {
	testNuGetStoreContract(t, newPostgresNuGetStore(t))
}

func TestPostgresNuGetSourceStoreContract(t *testing.T) {
	testNuGetSourceStoreContract(t, newPostgresNuGetStore(t))
}

func TestPostgresNuGetPaginationAndReopen(t *testing.T) {
	store := newPostgresNuGetStore(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "nuget-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "a.b", "a-b", "ab"} {
		for version := 1; version <= 3; version++ {
			s, _, err := store.ReserveNuGetPublication(ctx, NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: id, Version: fmt.Sprintf("%d.0.0", version), Digest: "sha256:" + fmt.Sprintf("%064x", version), Size: 1, Actor: "operator", ExpiresAt: time.Now().UTC().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			s.MetadataJSON = []byte(fmt.Sprintf(`{"id":%q,"version":%q}`, id, s.Version))
			s.Nuspec = []byte("original nuspec")
			if _, err = store.StageNuGetPublication(ctx, s); err != nil {
				t.Fatal(err)
			}
			if _, _, err = store.CommitNuGetPublication(ctx, s.ID, s.Actor); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newPostgresNuGetStore(t)
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := store.ListNuGetPublications(ctx, repo.ID, "", 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, p := range page {
			key := p.PackageID + "@" + p.Version
			if key <= cursor || seen[key] {
				t.Fatalf("non-monotonic cursor %s after %s", key, cursor)
			}
			cursor, seen[key] = key, true
		}
	}
	if len(seen) != 12 {
		t.Fatalf("reopened pagination lost packages: %v", seen)
	}
}
