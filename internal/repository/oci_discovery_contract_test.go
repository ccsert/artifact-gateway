package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/google/uuid"
)

func TestMemoryOCIDiscoverySnapshotAndBinding(t *testing.T) {
	s := NewMemoryStore()
	testOCIDiscoveryStore(t, s, s)
}

func testOCIDiscoveryStore(t *testing.T, store discovery.Store, repos HostedRepositoryStore) {
	t.Helper()
	ctx := context.Background()
	repo, err := repos.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "discovery-" + uuid.NewString()[:8], Format: FormatOCI, Type: RepositoryTypeProxy, Endpoint: "https://registry.example.invalid", AllowedHosts: []string{"registry.example.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	guard := discovery.Guard{Kind: "repository", ID: repo.ID, Version: repo.Version}
	snapshot := discovery.Snapshot{ID: uuid.NewString(), Owner: "principal-bound", Actor: "fixture-admin", Kind: "catalog", Guards: []discovery.Guard{guard}, Entries: []discovery.CatalogEntry{{Name: "proxy/team/image", Sources: []string{repo.Name}}}, Coverage: []discovery.Coverage{{Namespace: "proxy", State: "complete"}}, ExpiresAt: time.Now().UTC().Add(discovery.SnapshotTTL)}
	if err := store.SaveOCIDiscoverySnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOCIDiscoverySnapshot(ctx, snapshot.ID, "other-principal"); !errors.Is(err, discovery.ErrNotFound) {
		t.Fatalf("cross-principal snapshot: %v", err)
	}
	loaded, err := store.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner)
	if err != nil || loaded.Entries[0].Name != snapshot.Entries[0].Name {
		t.Fatalf("snapshot=%+v err=%v", loaded, err)
	}
	loaded.Entries[0].Name = "mutated"
	loaded, err = store.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner)
	if err != nil || loaded.Entries[0].Name == "mutated" {
		t.Fatal("snapshot returned caller-owned mutable state")
	}
	snapshot.Entries[0].Name = "replacement/image"
	if err := store.SaveOCIDiscoverySnapshot(ctx, snapshot); !errors.Is(err, discovery.ErrChanged) {
		t.Fatalf("snapshot replacement=%v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	ref := "sha256:" + strings.Repeat("b", 64)
	key := "oci/index/" + strings.Repeat("c", 64) + ".json"
	binding := discovery.Binding{Actor: "fixture-admin", Owner: discovery.OwnerForActor("fixture-admin"), Scope: "repository:" + repo.ID, Image: "team/image", Subject: digest, SourceID: repo.ID, SourceVersion: repo.Version, Guards: []discovery.Guard{guard}, FetchedAt: time.Now().UTC(), Objects: []discovery.Object{{Digest: digest, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 12, CacheKey: key, Resource: "manifests"}, {Digest: ref, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 15, CacheKey: key, Resource: "manifests"}}, Referrers: []discovery.Descriptor{{Digest: ref, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 15, ArtifactType: "application/example.sbom"}}}
	if err := store.PublishOCISubjectBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	got, object, err := store.GetOCIBoundObject(ctx, binding.Scope, binding.Owner, binding.Image, ref)
	if err != nil || got.SourceID != repo.ID || object.Digest != ref {
		t.Fatalf("bound object=%+v source=%+v err=%v", object, got, err)
	}
	if _, _, err := store.GetOCIBoundObject(ctx, binding.Scope, binding.Owner, "other/image", ref); !errors.Is(err, discovery.ErrNotFound) {
		t.Fatalf("cross image object=%v", err)
	}
	if _, _, err := store.GetOCIBoundObject(ctx, binding.Scope, discovery.OwnerForActor("another-reader"), binding.Image, ref); !errors.Is(err, discovery.ErrNotFound) {
		t.Fatalf("cross-principal object=%v", err)
	}
	if _, err := store.GetOCISubjectBinding(ctx, binding.Scope, discovery.OwnerForActor("another-reader"), binding.Image, digest); !errors.Is(err, discovery.ErrNotFound) {
		t.Fatalf("cross-principal binding=%v", err)
	}
	binding.Objects[1].Size++
	if err := store.PublishOCISubjectBinding(ctx, binding); !errors.Is(err, discovery.ErrInvalid) {
		t.Fatalf("contradictory descriptor=%v", err)
	}
	updated, err := repos.UpdateHostedRepository(ctx, repo, repo.Version)
	if err != nil || updated.Version == repo.Version {
		t.Fatalf("source update=%+v %v", updated, err)
	}
	if _, err := store.GetOCIDiscoverySnapshot(ctx, snapshot.ID, snapshot.Owner); !errors.Is(err, discovery.ErrChanged) {
		t.Fatalf("stale snapshot=%v", err)
	}
	binding.Objects[1].Size--
	if err := store.PublishOCISubjectBinding(ctx, binding); !errors.Is(err, discovery.ErrChanged) {
		t.Fatalf("stale source publication=%v", err)
	}
	got, err = store.GetOCISubjectBinding(ctx, binding.Scope, binding.Owner, binding.Image, binding.Subject)
	if err != nil || got.SourceVersion != repo.Version {
		t.Fatalf("source binding drifted=%+v %v", got, err)
	}
}
