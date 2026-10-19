package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type conanPublicationContractStore interface {
	HostedRepositoryStore
	NativeConanStore
	ConanNativePublicationStore
	RepositoryCapacityStore
}

func TestMemoryConanNativePublication(t *testing.T) {
	store := NewMemoryStore()
	exerciseConanNativePublication(t, store, func(id string) {
		store.mu.Lock()
		defer store.mu.Unlock()
		session := store.conanSessions[id]
		session.ExpiresAt = time.Now().Add(-time.Second)
		store.conanSessions[id] = session
	})
}

func exerciseConanNativePublication(t *testing.T, store conanPublicationContractStore, expire func(string)) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "conan-contract-" + uuid.NewString()[:8], Format: FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	incoming := ConanPublishSession{RepositoryID: repo.ID, Publisher: "machine", Kind: "recipe", Reference: "pkg/1.0/_/_", RecipeRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := store.BeginConanNativeSession(ctx, incoming)
			ids <- session.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("concurrent begin created multiple active sessions")
		}
	}
	stage := func(session ConanPublishSession, name, body string) ConanPublishSession {
		t.Helper()
		sum := sha256.Sum256([]byte(body))
		digest := fmt.Sprintf("sha256:%x", sum)
		key := "native/conan/sessions/" + session.ID + "/" + uuid.NewString() + "/" + name
		if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		next, err := store.StageConanNativeObject(ctx, session.ID, MavenDeclaredObject{Name: name, Digest: digest, Size: int64(len(body))}, key)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	session, err := store.GetConanPublishSession(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	session = stage(session, "conanfile.py", "initial")
	if err = store.MarkConanPublishObject(ctx, session.ID, "conanfile.py", "foreign-management-key"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("management changed a native snapshot: %v", err)
	}
	if err = store.CommitConanPublishSession(ctx, session.ID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("management committed a native session without manifest validation: %v", err)
	}
	staleVersion := session.Version
	session = stage(session, "conanfile.py", "corrected")
	session = stage(session, "conanmanifest.txt", "manifest")
	digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err = store.CommitConanNativeSession(ctx, session.ID, staleVersion, digest); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale snapshot commit=%v", err)
	}
	if _, err = store.GetConanRecipeRevision(ctx, repo.ID, incoming.Reference, incoming.RecipeRevision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale snapshot exposed recipe: %v", err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CommitConanNativeSession(ctx, session.ID, session.Version, digest); !IsQuotaExceeded(err) {
		t.Fatalf("quota publication=%v", err)
	}
	current, err := store.GetConanPublishSession(ctx, session.ID)
	if err != nil || current.State != "open" {
		t.Fatalf("failed commit changed session: %+v %v", current, err)
	}
	if _, err = store.GetConanRecipeRevision(ctx, repo.ID, incoming.Reference, incoming.RecipeRevision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quota rejected recipe visible: %v", err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	committed, replay, err := store.CommitConanNativeSession(ctx, session.ID, session.Version, digest)
	if err != nil || replay || committed.State != "committed" {
		t.Fatalf("commit=%+v replay=%t error=%v", committed, replay, err)
	}
	assets, err := store.ListConanRecipeAssets(ctx, repo.ID, incoming.Reference, incoming.RecipeRevision)
	if err != nil || len(assets) != 2 {
		t.Fatalf("committed stale asset membership=%v %v", assets, err)
	}
	for _, asset := range assets {
		if asset.Path == "conanfile.py" && asset.Size != 9 {
			t.Fatalf("committed obsolete file: %+v", asset)
		}
	}
	// Package publication and retention's recipe tombstone serialize on the
	// same closure. Whichever wins first, the final tombstone hides the package.
	pkgIncoming := incoming
	pkgIncoming.Kind, pkgIncoming.PackageID, pkgIncoming.PackageRevision = "package", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pkg, err := store.BeginConanNativeSession(ctx, pkgIncoming)
	if err != nil {
		t.Fatal(err)
	}
	pkg = stage(pkg, "conaninfo.txt", "info")
	pkg = stage(pkg, "conan_package.tgz", "package")
	pkg = stage(pkg, "conanmanifest.txt", "manifest")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, _, err := store.CommitConanNativeSession(ctx, pkg.ID, pkg.Version, digest)
		results <- err
	}()
	go func() {
		<-start
		_, err := store.TombstoneConanRecipeRevision(ctx, repo.ID, incoming.Reference, incoming.RecipeRevision)
		results <- err
	}()
	close(start)
	for range 2 {
		err := <-results
		if err != nil && !errors.Is(err, ErrArtifactTombstoned) && !errors.Is(err, ErrDisabled) {
			t.Fatalf("retention/publication race: %v", err)
		}
	}
	if revisions, err := store.ListConanPackageRevisions(ctx, repo.ID, incoming.Reference, incoming.RecipeRevision, pkgIncoming.PackageID); err != nil || len(revisions) != 0 {
		t.Fatalf("retention race exposed package: %+v %v", revisions, err)
	}

	// A live pending snapshot stays retained even when collection's cutoff is
	// advanced. Expiry fences the old writer; a retry receives a fresh session.
	incoming.Reference = "pending/1.0/_/_"
	pending, err := store.BeginConanNativeSession(ctx, incoming)
	if err != nil {
		t.Fatal(err)
	}
	pending = stage(pending, "conanfile.py", "pending")
	uploads, err := store.ListConanPublishUploads(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := uploads["conanfile.py"]
	reclaimable, err := store.ListReclaimableConanObjects(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reclaimable {
		if item.ObjectKey == key {
			t.Fatal("GC admitted a live pending publication")
		}
	}
	expire(pending.ID)
	fresh, err := store.BeginConanNativeSession(ctx, incoming)
	if err != nil || fresh.ID == pending.ID || len(fresh.Objects) != 0 {
		t.Fatalf("expired session reused: %+v %v", fresh, err)
	}
	if _, _, err = store.CommitConanNativeSession(ctx, pending.ID, pending.Version, digest); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expired writer publication=%v", err)
	}
	reclaimable, err = store.ListReclaimableConanObjects(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range reclaimable {
		if item.ObjectKey == key {
			found = true
		}
	}
	if !found {
		t.Fatal("expired staged object was retained indefinitely")
	}
}
