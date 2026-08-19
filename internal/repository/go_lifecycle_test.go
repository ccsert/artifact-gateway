package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryGoModuleVersionTombstoneAndRestoreControlVisibility(t *testing.T) {
	const (
		repositoryID = "go-lifecycle"
		modulePath   = "example.com/team/widget"
		version      = "v1.2.3"
		zipDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: repositoryID, Name: repositoryID, Format: FormatGo, Type: RepositoryTypeHosted}); err != nil {
		t.Fatal(err)
	}
	assets := []GoModuleAsset{
		{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "info", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ObjectKey: "native/go/info", Size: 1},
		{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "mod", Digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ObjectKey: "native/go/mod", Size: 2},
		{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "zip", Digest: zipDigest, ObjectKey: "native/go/zip", Size: 3},
	}
	if _, _, err := store.PublishGoModule(ctx, GoModulePublication{
		Version: GoModuleVersion{RepositoryID: repositoryID, Module: modulePath, Version: version, Publisher: "builder", PublishedAt: time.Now().UTC()},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.TombstoneGoModuleVersion(ctx, repositoryID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.PublishGoModule(ctx, GoModulePublication{
		Version: GoModuleVersion{RepositoryID: repositoryID, Module: modulePath, Version: version, Publisher: "builder", PublishedAt: time.Now().UTC()},
		Assets:  assets,
	}); !errors.Is(err, ErrArtifactTombstoned) {
		t.Fatalf("republish tombstoned version error=%v", err)
	}
	if _, err := store.TombstoneGoModuleVersion(ctx, repositoryID, modulePath, version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat tombstone error=%v", err)
	}
	for name, lookup := range map[string]func() error{
		"version": func() error { _, err := store.GetGoModuleVersion(ctx, repositoryID, modulePath, version); return err },
		"asset": func() error {
			_, err := store.GetGoModuleAsset(ctx, repositoryID, modulePath, version, "zip")
			return err
		},
		"list": func() error { _, err := store.ListGoModuleVersions(ctx, repositoryID, modulePath); return err },
	} {
		if err := lookup(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted %s error=%v", name, err)
		}
	}
	tombstone, err := store.GetArtifactTombstone(ctx, repositoryID, FormatGo, modulePath+"@"+version)
	if err != nil || tombstone.Digest != zipDigest {
		t.Fatalf("tombstone=%#v err=%v", tombstone, err)
	}
	search, err := store.SearchArtifactProjection(ctx, repositoryID, FormatGo, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: modulePath}, 10, ArtifactSearchPosition{})
	if err != nil || len(search) != 0 {
		t.Fatalf("deleted search=%#v err=%v", search, err)
	}
	identities, err := store.ListArtifactIdentities(ctx, repositoryID, FormatGo, ArtifactIdentityScan, "", 10)
	if err != nil || len(identities) != 0 {
		t.Fatalf("deleted identities=%#v err=%v", identities, err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repositoryID)
	if err != nil || capacity.UsedBytes != 6 || capacity.ObjectCount != 3 {
		t.Fatalf("tombstoned physical capacity=%#v err=%v", capacity, err)
	}

	if _, err = store.RestoreGoModuleVersion(ctx, repositoryID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RestoreGoModuleVersion(ctx, repositoryID, modulePath, version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat restore error=%v", err)
	}
	if _, err = store.GetGoModuleAsset(ctx, repositoryID, modulePath, version, "zip"); err != nil {
		t.Fatalf("restored asset: %v", err)
	}
	search, err = store.SearchArtifactProjection(ctx, repositoryID, FormatGo, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: modulePath}, 10, ArtifactSearchPosition{})
	if err != nil || len(search) != 1 || search[0].Version != version || search[0].Digest != zipDigest {
		t.Fatalf("restored search=%#v err=%v", search, err)
	}
}
