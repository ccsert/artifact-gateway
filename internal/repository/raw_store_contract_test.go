package repository_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type rawContractFactory func(*testing.T) (repository.NativeRawStore, string)

func TestRawMemoryStoreContract(t *testing.T) {
	runRawStoreContract(t, func(t *testing.T) (repository.NativeRawStore, string) {
		t.Helper()
		store := repository.NewMemoryStore()
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-contract-" + uuid.NewString()[:8], Format: repository.FormatRaw})
		if err != nil {
			t.Fatal(err)
		}
		return store, repo.ID
	})
}

// The same assertions run against PostgreSQL under the integration build tag.
// Every case gets its own repository and immutable digests, so no backend's
// internal maps or SQL schema are used to make one implementation pass.
func runRawStoreContract(t *testing.T, factory rawContractFactory) {
	t.Helper()
	asset := func(repoID, path, content string) repository.RawAsset {
		sum := sha256.Sum256([]byte(repoID + ":" + content))
		return repository.RawAsset{RepositoryID: repoID, Path: path, Digest: fmt.Sprintf("sha256:%x", sum), ObjectKey: fmt.Sprintf("native/raw/sha256/%x", sum), Size: int64(len(content)), ContentType: "text/plain"}
	}
	put := func(t *testing.T, store repository.NativeRawStore, value repository.RawAsset) repository.RawAsset {
		t.Helper()
		actual, err := store.PutRawAsset(context.Background(), value)
		if err != nil {
			t.Fatal(err)
		}
		if actual.RepositoryID != value.RepositoryID || actual.Path != value.Path || actual.Digest != value.Digest || actual.UpdatedAt.IsZero() {
			t.Fatalf("publication identity: %#v", actual)
		}
		return actual
	}
	t.Run("ordered_bounded_path_reads", func(t *testing.T) {
		store, repoID := factory(t)
		for _, path := range []string{"packages/c.txt", "packages/a.txt", "packages/b.txt", "other/a.txt"} {
			put(t, store, asset(repoID, path, path))
		}
		page, err := store.ListRawAssets(context.Background(), repoID, "packages/", 2, "")
		if err != nil || len(page) != 2 || page[0].Path != "packages/a.txt" || page[1].Path != "packages/b.txt" {
			t.Fatalf("first page: %#v %v", page, err)
		}
		page, err = store.ListRawAssets(context.Background(), repoID, "packages/", 2, "packages/b.txt")
		if err != nil || len(page) != 1 || page[0].Path != "packages/c.txt" {
			t.Fatalf("next page: %#v %v", page, err)
		}
		if _, err := store.GetRawAsset(context.Background(), repoID, "missing"); !errors.Is(err, repository.ErrNotFound) {
			t.Fatal(err)
		}
	})
	t.Run("default_page_is_bounded", func(t *testing.T) {
		store, repoID := factory(t)
		for n := 0; n < 101; n++ {
			put(t, store, asset(repoID, fmt.Sprintf("%03d", n), fmt.Sprint(n)))
		}
		page, err := store.ListRawAssets(context.Background(), repoID, "", 0, "")
		if err != nil || len(page) != 100 {
			t.Fatalf("default page: count=%d err=%v; want 100", len(page), err)
		}
	})
	t.Run("immutable_object_metadata_is_reused", func(t *testing.T) {
		store, repoID := factory(t)
		first := put(t, store, asset(repoID, "first.txt", "same"))
		second := first
		second.Path = "second.txt"
		second.ObjectKey = "untrusted/replacement"
		second.Size = 999
		result := put(t, store, second)
		if result.ObjectKey != first.ObjectKey || result.Size != first.Size {
			t.Fatalf("object replaced: %#v", result)
		}
		actual, err := store.GetRawAsset(context.Background(), repoID, second.Path)
		if err != nil || actual.ObjectKey != first.ObjectKey || actual.Size != first.Size {
			t.Fatalf("readback: %#v %v", actual, err)
		}
	})
	t.Run("conditional_reference_has_one_winner", func(t *testing.T) {
		store, repoID := factory(t)
		original := put(t, store, asset(repoID, "latest.txt", "old"))
		const writers = 8
		results := make(chan error, writers)
		start := make(chan struct{})
		for n := 0; n < writers; n++ {
			go func() {
				<-start
				_, err := store.PutRawAssetIf(context.Background(), asset(repoID, "latest.txt", fmt.Sprint(n)), repository.RawWriteCondition{MatchDigests: []string{original.Digest}})
				results <- err
			}()
		}
		close(start)
		successes := 0
		for n := 0; n < writers; n++ {
			err := <-results
			if err == nil {
				successes++
			} else if !errors.Is(err, repository.ErrPreconditionFailed) {
				t.Fatal(err)
			}
		}
		if successes != 1 {
			t.Fatalf("winners=%d; want 1", successes)
		}
		current, err := store.GetRawAsset(context.Background(), repoID, "latest.txt")
		if err != nil || current.Digest == original.Digest {
			t.Fatalf("current=%#v err=%v", current, err)
		}
		if _, err := store.PutRawAssetIf(context.Background(), original, repository.RawWriteCondition{RequireAbsent: true}); !errors.Is(err, repository.ErrPreconditionFailed) {
			t.Fatal(err)
		}
	})
	t.Run("upload_completion_is_atomic_with_condition", func(t *testing.T) {
		store, repoID := factory(t)
		current := put(t, store, asset(repoID, "resume.txt", "current"))
		next := asset(repoID, current.Path, "next")
		upload := repository.RawUpload{ID: uuid.NewString(), RepositoryID: repoID, Path: current.Path, ObjectKey: "staging/" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)}
		if _, err := store.CreateRawUpload(context.Background(), upload); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateRawUpload(context.Background(), upload.ID, next.Size); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteRawUploadIf(context.Background(), upload.ID, next, repository.RawWriteCondition{RequireAbsent: true}); !errors.Is(err, repository.ErrPreconditionFailed) {
			t.Fatal(err)
		}
		pending, err := store.GetRawUpload(context.Background(), upload.ID)
		if err != nil || pending.State != "open" || pending.Offset != next.Size {
			t.Fatalf("rejected upload=%#v %v", pending, err)
		}
		if _, err := store.CompleteRawUploadIf(context.Background(), upload.ID, next, repository.RawWriteCondition{MatchDigests: []string{current.Digest}}); err != nil {
			t.Fatal(err)
		}
		pending, err = store.GetRawUpload(context.Background(), upload.ID)
		if err != nil || pending.State != "completed" {
			t.Fatalf("completed upload=%#v %v", pending, err)
		}
		if _, err := store.CompleteRawUpload(context.Background(), upload.ID, next); !errors.Is(err, repository.ErrNotFound) {
			t.Fatal(err)
		}
		if err := store.MarkRawUploadCollected(context.Background(), upload.ID); err != nil {
			t.Fatal(err)
		}
		pending, err = store.GetRawUpload(context.Background(), upload.ID)
		if err != nil || pending.CollectedAt.IsZero() {
			t.Fatalf("collected upload=%#v %v", pending, err)
		}
	})
	t.Run("cancel_and_expire_reject_publication", func(t *testing.T) {
		store, repoID := factory(t)
		for _, expired := range []bool{false, true} {
			value := asset(repoID, uuid.NewString(), "pending")
			expiry := time.Now().Add(time.Hour)
			if expired {
				expiry = time.Now().Add(-time.Hour)
			}
			upload := repository.RawUpload{ID: uuid.NewString(), RepositoryID: repoID, Path: value.Path, ObjectKey: "staging/" + uuid.NewString(), State: "open", ExpiresAt: expiry}
			if _, err := store.CreateRawUpload(context.Background(), upload); err != nil {
				t.Fatal(err)
			}
			if expired {
				if _, err := store.UpdateRawUpload(context.Background(), upload.ID, 1); !errors.Is(err, repository.ErrNotFound) {
					t.Fatal(err)
				}
				expiredUploads, err := store.ExpireRawUploads(context.Background(), time.Now(), 100)
				if err != nil || !slices.ContainsFunc(expiredUploads, func(v repository.RawUpload) bool { return v.ID == upload.ID }) {
					t.Fatalf("expired=%#v err=%v", expiredUploads, err)
				}
			} else if _, err := store.CancelRawUpload(context.Background(), upload.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CompleteRawUpload(context.Background(), upload.ID, value); !errors.Is(err, repository.ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := store.GetRawAsset(context.Background(), repoID, value.Path); !errors.Is(err, repository.ErrNotFound) {
				t.Fatal(err)
			}
		}
	})
	t.Run("tombstone_restore_and_collection", func(t *testing.T) {
		store, repoID := factory(t)
		original := put(t, store, asset(repoID, "deleted.txt", "persist"))
		if err := store.DeleteRawAsset(context.Background(), repoID, original.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetRawAsset(context.Background(), repoID, original.Path); !errors.Is(err, repository.ErrNotFound) {
			t.Fatal(err)
		}
		restored, err := store.RestoreRawAsset(context.Background(), repoID, original.Path)
		if err != nil || restored.Digest != original.Digest {
			t.Fatalf("restore=%#v %v", restored, err)
		}
		if err := store.MarkRawObjectCollected(context.Background(), original.Digest); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("collected visible object: %v", err)
		}
		if err := store.DeleteRawAsset(context.Background(), repoID, original.Path); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkRawObjectCollected(context.Background(), original.Digest); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RestoreRawAsset(context.Background(), repoID, original.Path); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("restore collected bytes=%v; want not found", err)
		}
	})
	t.Run("default_reclaim_batch_is_bounded", func(t *testing.T) {
		store, repoID := factory(t)
		for n := 0; n < 101; n++ {
			value := asset(repoID, "", fmt.Sprint(n))
			if err := store.StageRawObject(context.Background(), repository.RawObject{RepositoryID: repoID, Digest: value.Digest, ObjectKey: value.ObjectKey, Size: value.Size}); err != nil {
				t.Fatal(err)
			}
		}
		candidates, err := store.ListUnreferencedRawObjects(context.Background(), time.Now().Add(time.Hour), 0)
		if err != nil || len(candidates) != 100 {
			t.Fatalf("default reclaim: count=%d err=%v; want 100", len(candidates), err)
		}
	})
	t.Run("raw_locks_honor_cancellation", func(t *testing.T) {
		store, _ := factory(t)
		for _, lock := range []struct {
			name    string
			acquire func(context.Context, string) (func(), error)
		}{{"upload", store.LockRawUpload}, {"object", store.LockRawObject}} {
			t.Run(lock.name, func(t *testing.T) {
				key := uuid.NewString()
				release, err := lock.acquire(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				var once sync.Once
				defer once.Do(release)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					unlock, err := lock.acquire(ctx, key)
					if err == nil {
						unlock()
					}
					done <- err
				}()
				select {
				case err := <-done:
					t.Fatalf("contended lock returned before owner released: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled lock=%v", err)
					}
				case <-time.After(time.Second):
					once.Do(release)
					<-done
					t.Fatal("canceled waiter blocked until current owner released")
				}
				once.Do(release)
				unlock, err := lock.acquire(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				unlock()
			})
		}
	})
}
