package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestNPMVersionTombstoneHidesAndRestoresArtifact(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "npm-lifecycle", Name: "npm-lifecycle", Format: FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	version := NPMVersion{
		RepositoryID: repo.ID, PackageName: "@scope/widget", Version: "1.2.3",
		Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Integrity: "sha512-YQ==", Shasum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TarballName: "scope-widget-1.2.3.tgz", ObjectKey: "native/npm/sha256/aaaaaaaa", Size: 8,
		Manifest: json.RawMessage(`{"name":"@scope/widget","version":"1.2.3"}`), Publisher: "release-bot",
	}
	if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
		t.Fatal(err)
	}
	coordinate := version.PackageName + "@" + version.Version
	deleted, err := store.TombstoneNPMVersion(ctx, repo.ID, version.PackageName, version.Version)
	if err != nil || deleted.State != "deleted" {
		t.Fatalf("tombstone=%#v err=%v", deleted, err)
	}
	if _, err = store.GetNPMPackage(ctx, repo.ID, version.PackageName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tombstoned package remained visible: %v", err)
	}
	tombstone, err := store.GetArtifactTombstone(ctx, repo.ID, FormatNPM, coordinate)
	if err != nil || tombstone.Digest != version.Digest {
		t.Fatalf("artifact tombstone=%#v err=%v", tombstone, err)
	}
	restored, err := store.RestoreNPMVersion(ctx, repo.ID, version.PackageName, version.Version)
	if err != nil || restored.State != "visible" {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	pkg, err := store.GetNPMPackage(ctx, repo.ID, version.PackageName)
	if err != nil || len(pkg.Versions) != 1 || pkg.Versions[0].Version != version.Version {
		t.Fatalf("restored package=%#v err=%v", pkg, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, repo.ID, FormatNPM, coordinate); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore kept tombstone: %v", err)
	}
}
