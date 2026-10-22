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

type helmContractStore interface {
	NativeHelmStore
	HostedRepositoryStore
	RepositoryCapacityStore
}

func TestHelmMemoryStoreContract(t *testing.T) { testHelmStoreContract(t, NewMemoryStore()) }
func testHelmStoreContract(t *testing.T, store helmContractStore) {
	t.Helper()
	ctx := t.Context()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "helm-" + uuid.NewString(), Format: FormatHelm, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 100); err != nil {
		t.Fatal(err)
	}
	in := HelmSession{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "owned", Version: "1.0.0", Digest: "sha256:" + fmt.Sprintf("%064x", 1), Size: 60, ProvenanceDigest: "sha256:" + fmt.Sprintf("%064x", 2), ProvenanceSize: 10, Actor: "publisher", MetadataJSON: []byte(`{"name":"owned","version":"1.0.0"}`), ChartYAML: []byte("original chart metadata"), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	reserved, replay, err := store.ReserveHelmPublication(ctx, in)
	if err != nil || replay || reserved.State != "pending" || reserved.ProvenanceKey == "" {
		t.Fatalf("reserve=%+v %t %v", reserved, replay, err)
	}
	if _, err = store.GetHelmPublication(ctx, repo.ID, in.Name, in.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending visible: %v", err)
	}
	if all, err := store.ListHelmPublications(ctx, repo.ID, 10); err != nil || len(all) != 0 {
		t.Fatalf("pending indexed: %+v %v", all, err)
	}
	other := in
	other.ID = uuid.NewString()
	other.Version = "2.0.0"
	other.MetadataJSON = []byte(`{"name":"owned","version":"2.0.0"}`)
	if _, _, err = store.ReserveHelmPublication(ctx, other); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("reservation quota: %v", err)
	}
	different := in
	different.ID = uuid.NewString()
	different.ProvenanceDigest = "sha256:" + fmt.Sprintf("%064x", 3)
	if _, _, err = store.ReserveHelmPublication(ctx, different); !errors.Is(err, ErrHelmPublicationConflict) {
		t.Fatalf("closure conflict: %v", err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, e := store.CommitHelmPublication(ctx, reserved.ID, "publisher")
			if e != nil {
				failures <- e
			} else if p.SessionID != reserved.ID || p.ProvenanceDigest != in.ProvenanceDigest {
				failures <- fmt.Errorf("different publication: %+v", p)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 70 || capacity.ObjectCount != 2 {
		t.Fatalf("capacity=%+v %v", capacity, err)
	}
	replayed, yes, err := store.ReserveHelmPublication(ctx, in)
	if err != nil || !yes || replayed.State != "committed" {
		t.Fatalf("replay=%+v %t %v", replayed, yes, err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 200); err != nil {
		t.Fatal(err)
	}
	pending, _, err := store.ReserveHelmPublication(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.ExpireHelmSessions(ctx, time.Now().Add(2*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range expired {
		if candidate.ID == reserved.ID {
			t.Fatal("visible object expired")
		}
		if candidate.ID == pending.ID {
			found = true
			if candidate.State != "expired" {
				t.Fatal("wrong expiry state")
			}
		}
	}
	if !found {
		t.Fatal("pending reservation did not expire")
	}
	if _, _, err = store.CommitHelmPublication(ctx, pending.ID, "publisher"); !errors.Is(err, ErrHelmSessionExpired) {
		t.Fatalf("expired commit: %v", err)
	}
	if err = store.MarkHelmSessionCollected(ctx, pending.ID, pending.ObjectKey); err != nil {
		t.Fatal(err)
	}
	collected, err := store.GetHelmSession(ctx, pending.ID)
	if err != nil || collected.CollectedAt.IsZero() {
		t.Fatalf("collection marker=%+v %v", collected, err)
	}
	other.ID = uuid.NewString()
	replacement, _, err := store.ReserveHelmPublication(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ObjectKey == pending.ObjectKey || replacement.ProvenanceKey == pending.ProvenanceKey {
		t.Fatal("replacement reused collected object key")
	}
}
func TestHelmPreviewNeverEntersAdmittedFormatCatalog(t *testing.T) {
	if IsSupportedFormat(FormatHelm) || FormatSupportsRepositoryType(FormatHelm, RepositoryTypeHosted) {
		t.Fatal("preview admitted")
	}
}
func TestHelmMemoryPublicationLockHonorsCancellation(t *testing.T) {
	store := NewMemoryStore()
	ctx, release, err := store.LockHelmPublication(t.Context(), "repo", "owned", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = store.LockHelmPublication(cancelled, "repo", "owned", "1.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lock=%v", err)
	}
}
