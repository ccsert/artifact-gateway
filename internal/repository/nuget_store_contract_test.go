package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nuGetContractStore interface {
	NativeNuGetStore
	HostedRepositoryStore
	RepositoryCapacityStore
}

func TestNuGetMemoryStoreContract(t *testing.T) { testNuGetStoreContract(t, NewMemoryStore()) }

func testNuGetStoreContract(t *testing.T, store nuGetContractStore) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "nuget-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	a := NuGetSession{ID: uuid.NewString(), RepositoryID: repo.ID, PackageID: "example", Version: "1.0.0", Digest: "sha256:" + fmt.Sprintf("%064x", 1), Size: 60, Actor: "publisher", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	reserved, replayed, err := store.ReserveNuGetPublication(ctx, a)
	if err != nil || replayed || reserved.State != "reserved" {
		t.Fatalf("reserve=%+v replay=%t err=%v", reserved, replayed, err)
	}
	if _, err = store.GetNuGetPublication(ctx, repo.ID, a.PackageID, a.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reserved visible: %v", err)
	}
	b := a
	b.ID = uuid.NewString()
	b.Version = "2.0.0"
	if _, _, err = store.ReserveNuGetPublication(ctx, b); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("reservation exceeded quota: %v", err)
	}
	changed := a
	changed.ID = uuid.NewString()
	changed.Digest = "sha256:" + fmt.Sprintf("%064x", 2)
	if _, _, err = store.ReserveNuGetPublication(ctx, changed); !errors.Is(err, ErrNuGetPublicationConflict) {
		t.Fatalf("identity conflict=%v", err)
	}
	reserved.Nuspec = []byte("original nuspec")
	reserved.MetadataJSON = []byte(`{"id":"Example","version":"1.0.0","dependencyGroups":[]}`)
	staged, err := store.StageNuGetPublication(ctx, reserved)
	if err != nil || staged.State != "staged" {
		t.Fatalf("stage=%+v err=%v", staged, err)
	}
	if got, err := store.ListNuGetPublications(ctx, repo.ID, "", 10, ""); err != nil || len(got) != 0 {
		t.Fatalf("staged visible: %+v %v", got, err)
	}
	var wg sync.WaitGroup
	results := make(chan NuGetPublication, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := store.CommitNuGetPublication(ctx, reserved.ID, reserved.Actor)
			if err != nil {
				failures <- err
				return
			}
			results <- p
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent commit: %v", err)
	}
	var first NuGetPublication
	for p := range results {
		if first.PublishedAt.IsZero() {
			first = p
		}
		if !p.PublishedAt.Equal(first.PublishedAt) || p.ObjectKey != first.ObjectKey || p.Digest != a.Digest {
			t.Fatalf("inconsistent commit=%+v first=%+v", p, first)
		}
	}
	got, err := store.ListNuGetPublications(ctx, repo.ID, "example", 10, "")
	if err != nil || len(got) != 1 || got[0].Digest != a.Digest {
		t.Fatalf("publications=%+v err=%v", got, err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 60 || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
	if _, replayed, err := store.ReserveNuGetPublication(ctx, a); err != nil || !replayed {
		t.Fatalf("lost-response replay=%t err=%v", replayed, err)
	}
	_, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := store.ReserveNuGetPublication(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ExpireNuGetSessions(ctx, time.Now().UTC().Add(2*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CommitNuGetPublication(ctx, old.ID, old.Actor); !errors.Is(err, ErrNuGetSessionExpired) {
		t.Fatalf("expired commit=%v", err)
	}
	b.ID = uuid.NewString()
	replacement, _, err := store.ReserveNuGetPublication(ctx, b)
	if err != nil || replacement.ObjectKey == old.ObjectKey {
		t.Fatalf("replacement=%+v err=%v", replacement, err)
	}
	if IsSupportedFormat(FormatNuGet) || FormatSupportsRepositoryType(FormatNuGet, RepositoryTypeHosted) {
		t.Fatal("preview admitted as public format")
	}
}
