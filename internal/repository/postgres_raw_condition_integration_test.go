//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresRawConditionalWritesAdmitOneWriter(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "raw-cond-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	// Digests are unique per run; losing writers leave staged objects that the
	// cleanup marks collected so other tests see no stray unreferenced rows.
	staged := make([]string, 0)
	t.Cleanup(func() {
		for _, digest := range staged {
			_ = store.MarkRawObjectCollected(context.Background(), digest)
		}
	})
	asset := func(path string, n int) RawAsset {
		sum := sha256.Sum256([]byte(fmt.Sprint(repo.ID, n)))
		digest := "sha256:" + hex.EncodeToString(sum[:])
		staged = append(staged, digest)
		key := "native/raw/sha256/" + strings.TrimPrefix(digest, "sha256:")
		if err := store.StageRawObject(ctx, RawObject{RepositoryID: repo.ID, Digest: digest, ObjectKey: key, Size: int64(n)}); err != nil {
			t.Fatal(err)
		}
		return RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: key, Size: int64(n), ContentType: "text/plain"}
	}
	race := func(path string, condition RawWriteCondition, first int) (int, []error) {
		const writers = 8
		candidates := make([]RawAsset, writers)
		for i := range candidates {
			candidates[i] = asset(path, first+i)
		}
		errs := make([]error, writers)
		var wg sync.WaitGroup
		for i := range candidates {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = store.PutRawAssetIf(ctx, candidates[i], condition)
			}()
		}
		wg.Wait()
		winner := -1
		for i, err := range errs {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("%s: writers %d and %d both succeeded", path, winner, i)
				}
				winner = i
			} else if !errors.Is(err, ErrPreconditionFailed) {
				t.Fatalf("%s: writer %d: %v", path, i, err)
			}
		}
		if winner < 0 {
			t.Fatalf("%s: no writer succeeded", path)
		}
		current, err := store.GetRawAsset(ctx, repo.ID, path)
		if err != nil || current.Digest != candidates[winner].Digest {
			t.Fatalf("%s: current=%#v err=%v want %s", path, current, err, candidates[winner].Digest)
		}
		return winner, errs
	}

	// Create-only on an absent path races on the insert, not on a locked row.
	race("releases/create-only.txt", RawWriteCondition{RequireAbsent: true}, 1)

	seed := asset("releases/if-match.txt", 100)
	if _, err := store.PutRawAsset(ctx, seed); err != nil {
		t.Fatal(err)
	}
	race("releases/if-match.txt", RawWriteCondition{MatchDigests: []string{seed.Digest}}, 101)
	if _, err := store.PutRawAssetIf(ctx, asset("releases/if-match.txt", 200), RawWriteCondition{MatchDigests: []string{seed.Digest}}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale validator after race: %v", err)
	}

	// A rejected resumable commit rolls back and leaves the upload open.
	uploadID := uuid.NewString()
	if _, err := store.CreateRawUpload(ctx, RawUpload{ID: uploadID, RepositoryID: repo.ID, Path: "releases/if-match.txt", ObjectKey: "native/raw/uploads/" + uploadID, State: "open", ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetRawAsset(ctx, repo.ID, "releases/if-match.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRawUploadIf(ctx, uploadID, asset("releases/if-match.txt", 300), RawWriteCondition{RequireAbsent: true}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("create-only complete over existing path: %v", err)
	}
	if upload, err := store.GetRawUpload(ctx, uploadID); err != nil || upload.State != "open" {
		t.Fatalf("upload after rejected complete=%#v err=%v", upload, err)
	}
	if after, err := store.GetRawAsset(ctx, repo.ID, "releases/if-match.txt"); err != nil || after.Digest != before.Digest {
		t.Fatalf("reference after rejected complete=%#v err=%v", after, err)
	}
	if _, err := store.CompleteRawUploadIf(ctx, uploadID, asset("releases/if-match.txt", 301), RawWriteCondition{MatchDigests: []string{before.Digest}}); err != nil {
		t.Fatalf("complete with current validator: %v", err)
	}
}
