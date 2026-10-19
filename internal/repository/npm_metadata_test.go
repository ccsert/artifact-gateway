package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type npmGovernanceTestStore interface {
	NativeNPMStore
	NPMMetadataStore
	NPMRestoreStore
	NPMCollectionStore
}

func exerciseNPMMetadataContract(t *testing.T, store npmGovernanceTestStore, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	const name = "@scope/governed"
	publishNPMDistTagFixture(t, store, repositoryID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
	publishNPMDistTagFixture(t, store, repositoryID, name, "1.1.0", map[string]string{"next": "1.1.0"})
	first, err := store.GetNPMVersion(ctx, repositoryID, name, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || pkg.Revision < 1 {
		t.Fatalf("initial revision=%d err=%v", pkg.Revision, err)
	}
	mutation := NPMMetadataMutation{RepositoryID: repositoryID, PackageName: name, ExpectedRevision: pkg.Revision,
		Deprecations: map[string]string{"1.0.0": "Use >=1.1.0 <script>plain text</script>"},
		Audit:        npmDistTagMutation(repositoryID, name, "", "").Audit}
	mutation.Audit.Operation = "npm.deprecate"
	changed, err := store.UpdateNPMMetadata(ctx, mutation)
	if err != nil || !changed.Changed || changed.Revision <= pkg.Revision {
		t.Fatalf("deprecate=%#v %v", changed, err)
	}
	current, err := store.GetNPMVersion(ctx, repositoryID, name, "1.0.0")
	want := first
	want.Deprecated = mutation.Deprecations["1.0.0"]
	if err != nil || !reflect.DeepEqual(current, want) {
		t.Fatalf("immutable identity/manifest changed: %#v want %#v err=%v", current, want, err)
	}
	// A lost response cannot replay a stale full packument over a newer state.
	if _, err = store.UpdateNPMMetadata(ctx, mutation); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale response replay=%v", err)
	}
	mutation.ExpectedRevision = changed.Revision
	repeated, err := store.UpdateNPMMetadata(ctx, mutation)
	if err != nil || repeated.Changed || repeated.Revision != changed.Revision {
		t.Fatalf("repeat=%#v %v", repeated, err)
	}
	for _, messages := range []map[string]string{
		{"1.0.0": "clear me", "9.9.9": "unknown"}, {"1.0.0": strings.Repeat("x", NPMDeprecationLimit+1)}, {"1.0.0": "bad\x1b[31m"},
	} {
		mutation.Deprecations = messages
		if _, err = store.UpdateNPMMetadata(ctx, mutation); err == nil {
			t.Fatalf("invalid/partial mutation succeeded: %#v", messages)
		}
		stored, _ := store.GetNPMVersion(ctx, repositoryID, name, "1.0.0")
		if stored.Deprecated != want.Deprecated {
			t.Fatal("partial failure changed metadata")
		}
	}
	mutation.Deprecations = map[string]string{"1.0.0": ""}
	cleared, err := store.UpdateNPMMetadata(ctx, mutation)
	if err != nil {
		t.Fatal(err)
	}
	mutation.ExpectedRevision, mutation.Deprecations = cleared.Revision, nil
	mutation.Unpublish, mutation.DistTags = []string{"1.0.0"}, map[string]string{"latest": "1.1.0", "next": "1.1.0"}
	mutation.Audit.Operation = "npm.unpublish"
	deleted, err := store.UpdateNPMMetadata(ctx, mutation)
	if err != nil || !deleted.Changed {
		t.Fatalf("unpublish=%#v %v", deleted, err)
	}
	if _, err = store.GetNPMVersion(ctx, repositoryID, name, "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted visible: %v", err)
	}
	pkg, err = store.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || len(pkg.Versions) != 1 || len(pkg.TombstonedVersions) != 1 || pkg.DistTags["latest"] != "1.1.0" {
		t.Fatalf("atomic tags/tombstone=%#v %v", pkg, err)
	}
	if _, err = store.PublishNPMVersion(ctx, first, nil); !errors.Is(err, ErrNameExists) {
		t.Fatalf("deleted identity reusable: %v", err)
	}
	restored, err := store.RestoreNPMVersion(ctx, NPMRestoreRequest{RepositoryID: repositoryID, PackageName: name, Version: "1.0.0"})
	if err != nil || restored.Digest != first.Digest || restored.ObjectKey != first.ObjectKey || restored.Size != first.Size || !reflect.DeepEqual(restored.Manifest, first.Manifest) {
		t.Fatalf("restore changed identity=%#v %v", restored, err)
	}
	pkg, _ = store.GetNPMPackage(ctx, repositoryID, name)
	mutation.ExpectedRevision, mutation.Unpublish, mutation.DistTags = pkg.Revision, []string{"1.0.0", "1.1.0"}, map[string]string{}
	if _, err = store.UpdateNPMMetadata(ctx, mutation); err != nil {
		t.Fatal(err)
	}
	pkg, err = store.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || len(pkg.Versions) != 0 || len(pkg.TombstonedVersions) != 2 || len(pkg.DistTags) != 0 {
		t.Fatalf("whole package barrier=%#v %v", pkg, err)
	}
	// A live reference in another Repository protects the shared CAS object.
	other, err := store.(interface {
		CreateHostedRepository(context.Context, HostedRepository) (HostedRepository, error)
	}).CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "npm-shared-" + uuid.NewString(), Format: FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	shared := first
	shared.RepositoryID = other.ID
	if _, err = store.PublishNPMVersion(ctx, shared, map[string]string{"latest": first.Version}); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkNPMObjectCollecting(ctx, first.ObjectKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("live shared reference admitted collection: %v", err)
	}
	if _, err = store.RestoreNPMVersion(ctx, NPMRestoreRequest{RepositoryID: repositoryID, PackageName: name, Version: first.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneNPMVersion(ctx, repositoryID, name, first.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneNPMVersion(ctx, other.ID, name, first.Version); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkNPMObjectCollecting(ctx, first.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RestoreNPMVersion(ctx, NPMRestoreRequest{RepositoryID: repositoryID, PackageName: name, Version: first.Version}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("collection fence allowed restore: %v", err)
	}
	if err = store.MarkNPMObjectCollected(ctx, first.ObjectKey); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryNPMMetadataContract(t *testing.T) {
	exerciseNPMMetadataContract(t, NewMemoryStore(), "npm-governance")
}

func exerciseNPMMetadataPublishRace(t *testing.T, first, second npmGovernanceTestStore, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	const name = "publish-unpublish-race"
	version := publishNPMDistTagFixture(t, first, repositoryID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
	pkg, err := first.GetNPMPackage(ctx, repositoryID, name)
	if err != nil {
		t.Fatal(err)
	}
	next := version
	next.Version, next.TarballName = "1.1.0", "widget-1.1.0.tgz"
	next.Manifest = json.RawMessage(`{"name":"` + name + `","version":"1.1.0"}`)
	sum := sha256.Sum256([]byte(repositoryID + name + next.Version))
	next.Digest = "sha256:" + hex.EncodeToString(sum[:])
	next.ObjectKey = "native/npm/sha256/" + hex.EncodeToString(sum[:])
	next.CreatedAt, next.CachedAt = time.Time{}, time.Time{}
	start := make(chan struct{})
	publication, withdrawal := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, e := first.PublishNPMVersion(ctx, next, map[string]string{"latest": next.Version})
		publication <- e
	}()
	go func() {
		<-start
		_, e := second.UpdateNPMMetadata(ctx, NPMMetadataMutation{RepositoryID: repositoryID, PackageName: name, ExpectedRevision: pkg.Revision, Unpublish: []string{version.Version}, DistTags: map[string]string{}, Audit: npmDistTagMutation(repositoryID, name, "", "").Audit})
		withdrawal <- e
	}()
	close(start)
	if err = <-publication; err != nil {
		t.Fatalf("concurrent publication=%v", err)
	}
	deleteErr := <-withdrawal
	if deleteErr != nil && !errors.Is(deleteErr, ErrVersionConflict) {
		t.Fatalf("concurrent unpublish=%v", deleteErr)
	}
	current, err := second.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || current.DistTags["latest"] != next.Version {
		t.Fatalf("new publication/tag lost: %#v %v", current, err)
	}
	if published, readErr := second.GetNPMVersion(ctx, repositoryID, name, next.Version); readErr != nil || published.Digest != next.Digest {
		t.Fatalf("new version lost or identity changed: %#v %v", published, readErr)
	}
	_, oldErr := first.GetNPMVersion(ctx, repositoryID, name, version.Version)
	if deleteErr == nil && !errors.Is(oldErr, ErrNotFound) || deleteErr != nil && oldErr != nil {
		t.Fatalf("unpublish=%v old version=%v", deleteErr, oldErr)
	}
}

func TestMemoryNPMMetadataPublishRace(t *testing.T) {
	store := NewMemoryStore()
	exerciseNPMMetadataPublishRace(t, store, store, "npm-publish-race")
}
