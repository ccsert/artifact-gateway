//go:build integration

package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresArtifactBrowseProjectsMavenAndRaw(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mavenRepo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "browse-maven-" + uuid.NewString(), Format: FormatMaven,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	rawRepo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "browse-raw-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	rawDigest := "sha256:" + strings.Repeat("d", 64)
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_maven_assets WHERE repository_id=$1`, mavenRepo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_maven_artifacts WHERE repository_id=$1`, mavenRepo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_assets WHERE repository_id=$1`, rawRepo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_objects WHERE digest=$1`, rawDigest)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, mavenRepo.ID, rawRepo.ID)
		_ = store.Close()
	})

	mavenDigest := "sha256:" + strings.Repeat("a", 64)
	coordinate := "com.acme:widget:1.0"
	assetPath := "com/acme/widget/1.0/widget-1.0.jar"
	if _, err = store.db.ExecContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible')`, uuid.NewString(), mavenRepo.ID, coordinate, mavenDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, mavenRepo.ID, assetPath, "browse/maven/"+uuid.NewString(), mavenDigest, 42); err != nil {
		t.Fatal(err)
	}

	namespaces, err := store.ListArtifactBrowseNodes(ctx, mavenRepo.ID, FormatMaven, ArtifactBrowseParent{}, 50, "")
	if err != nil || len(namespaces) != 1 || namespaces[0].Name != "com.acme" {
		t.Fatalf("maven namespaces=%#v err=%v", namespaces, err)
	}
	components, err := store.ListArtifactBrowseNodes(ctx, mavenRepo.ID, FormatMaven, ArtifactBrowseParent{Kind: BrowseNodeNamespace, Namespace: "com.acme"}, 50, "")
	if err != nil || len(components) != 1 || components[0].Name != "widget" {
		t.Fatalf("maven components=%#v err=%v", components, err)
	}
	versions, err := store.ListArtifactBrowseNodes(ctx, mavenRepo.ID, FormatMaven, ArtifactBrowseParent{Kind: BrowseNodeComponent, Namespace: "com.acme", Component: "widget"}, 50, "")
	if err != nil || len(versions) != 1 || versions[0].Coordinate != coordinate {
		t.Fatalf("maven versions=%#v err=%v", versions, err)
	}
	assets, err := store.ListArtifactBrowseNodes(ctx, mavenRepo.ID, FormatMaven, ArtifactBrowseParent{Kind: BrowseNodeVersion, Namespace: "com.acme", Component: "widget", Version: coordinate}, 50, "")
	if err != nil || len(assets) != 1 || assets[0].Path != assetPath || assets[0].Size != 42 {
		t.Fatalf("maven assets=%#v err=%v", assets, err)
	}

	if _, err = store.PutRawAsset(ctx, RawAsset{
		RepositoryID: rawRepo.ID, Path: "docs/release%20notes.txt", Digest: rawDigest,
		ObjectKey: "browse/raw/" + uuid.NewString(), Size: 0, ContentType: "text/plain",
	}); err != nil {
		t.Fatal(err)
	}
	rawRoot, err := store.ListArtifactBrowseNodes(ctx, rawRepo.ID, FormatRaw, ArtifactBrowseParent{}, 50, "")
	if err != nil || len(rawRoot) != 1 || rawRoot[0].Name != "docs" || !rawRoot[0].HasChildren {
		t.Fatalf("raw root=%#v err=%v", rawRoot, err)
	}
	rawAssets, err := store.ListArtifactBrowseNodes(ctx, rawRepo.ID, FormatRaw, ArtifactBrowseParent{Kind: BrowseNodeDirectory, Path: "docs"}, 50, "")
	if err != nil || len(rawAssets) != 1 || rawAssets[0].Path != "docs/release%20notes.txt" || rawAssets[0].Size != 0 || rawAssets[0].ContentType != "text/plain" {
		t.Fatalf("raw assets=%#v err=%v", rawAssets, err)
	}
}
