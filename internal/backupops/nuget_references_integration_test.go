//go:build integration

package backupops

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresNuGetPublishedBackupReferences(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-backup-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		s, _, err := store.ReserveNuGetPublication(t.Context(), repository.NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: "backup", Version: version, Digest: "sha256:" + strings.Repeat("1", 64), Size: 1, Actor: "operator", ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		s.MetadataJSON = []byte(`{"id":"backup","version":"` + version + `"}`)
		s.Nuspec = []byte("original nuspec")
		if _, err = store.StageNuGetPublication(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, s.ObjectKey)
		if version == "1.0.0" {
			if _, _, err = store.CommitNuGetPublication(t.Context(), s.ID, s.Actor); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	if err = (Postgres{DSN: dsn}).WalkReferences(t.Context(), func(key string) error { seen[key] = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !seen[keys[0]] || seen[keys[1]] {
		t.Fatalf("visible/staged backup references: %t/%t", seen[keys[0]], seen[keys[1]])
	}
}
