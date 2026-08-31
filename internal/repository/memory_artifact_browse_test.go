package repository

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryArtifactBrowsePinsMavenSnapshotAssetsToSelectedBuild(t *testing.T) {
	store := NewMemoryStore()
	coordinate := "org.example:widget:1.0-SNAPSHOT"
	createdOne := time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC)
	createdTwo := createdOne.Add(time.Minute)
	builds := []MavenArtifact{
		{ID: "build-one", RepositoryID: "repo", Coordinate: coordinate, Digest: "sha256:" + strings.Repeat("1", 64), State: "visible", BuildNumber: 1, CreatedAt: createdOne},
		{ID: "build-two", RepositoryID: "repo", Coordinate: coordinate, Digest: "sha256:" + strings.Repeat("2", 64), State: "visible", BuildNumber: 2, CreatedAt: createdTwo},
	}
	for _, build := range builds {
		store.mavenArtifacts[build.ID] = build
		path := mavenArtifactPathPrefix(coordinate) + mavenSnapshotBuildFilePrefix(coordinate, build.CreatedAt, build.BuildNumber) + ".jar"
		store.mavenAssets["repo\x00"+path] = MavenAsset{RepositoryID: "repo", Path: path, Digest: build.Digest, Size: int64(build.BuildNumber)}
	}

	versions, err := store.ListArtifactBrowseNodes(context.Background(), "repo", FormatMaven, ArtifactBrowseParent{
		Kind: BrowseNodeComponent, Namespace: "org.example", Component: "widget",
	}, 50, "")
	if err != nil || len(versions) != 1 || versions[0].BuildNumber != 2 || versions[0].Digest != builds[1].Digest {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}

	assets, err := store.ListArtifactBrowseNodes(context.Background(), "repo", FormatMaven, ArtifactBrowseParent{
		Kind: BrowseNodeVersion, Namespace: "org.example", Component: "widget",
		Version: coordinate, BuildNumber: versions[0].BuildNumber,
	}, 50, "")
	if err != nil || len(assets) != 1 || assets[0].Digest != builds[1].Digest || assets[0].Size != 2 {
		t.Fatalf("assets=%#v err=%v", assets, err)
	}
}
