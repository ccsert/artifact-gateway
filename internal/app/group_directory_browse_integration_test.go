//go:build integration

package app

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func groupDirectoryOnlyItem(t *testing.T, handler http.Handler, groupID, parent string) adminopenapi.BrowseNode {
	t.Helper()
	response, page := groupDirectoryRequest(t, handler, testAuthenticator().AdminToken, groupID, parent, "", 50)
	if response.Code != 200 || len(page.Items) != 1 {
		t.Fatalf("directory: %d %s", response.Code, response.Body)
	}
	return page.Items[0]
}

func TestGroupDirectoryPostgresMavenGroupOnlyCache(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	control, err := NewPostgresCacheControlStore(NewMemoryOCIObjectStore(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	f := newGroupMavenFixture(t, control, 404, 200)
	f.group = newBrowseGroup(t, f.store, repository.FormatMaven, f.repos...)
	t.Cleanup(func() { _ = control.Delete(context.Background(), f.cache.Key(f.group.Name, groupMavenPath)) })
	f.expectGet(200, f.repos[1].Name)
	oci := NewDefaultOCICache(control, nil)
	handler := NewGatewayHandlerWithCacheMaintenance(Dependencies{}, f.store, TestAdapter{}, testAuthenticator(), oci, f.cache, NewCacheMaintenance(control, oci))
	namespace := groupDirectoryOnlyItem(t, handler, f.group.ID, "")
	component := groupDirectoryOnlyItem(t, handler, f.group.ID, namespace.Id)
	version := groupDirectoryOnlyItem(t, handler, f.group.ID, component.Id)
	asset := groupDirectoryOnlyItem(t, handler, f.group.ID, version.Id)
	if asset.Sources == nil || len(*asset.Sources) != 1 {
		t.Fatalf("sources=%+v", asset)
	}
	source := (*asset.Sources)[0]
	if source.RepositoryId.String() != f.repos[1].ID || source.ResolutionOrder != 2 || source.CacheRepositoryName == nil || *source.CacheRepositoryName != f.group.Name {
		t.Fatalf("source=%+v", source)
	}
	// No request ever hit a member URL, and directory expansion makes no network calls.
	if f.upstreams[0].calls.Load() != 1 || f.upstreams[1].calls.Load() != 1 {
		t.Fatal("browse accessed upstream")
	}
	_, direct := browseRepositoryForTest(t, handler, testAuthenticator().AdminToken, f.repos[1].ID, "", "", 50)
	if len(direct.Items) != 0 {
		t.Fatalf("Group index incorrectly became member index: %+v", direct)
	}
	f.replaceMembers(1, 0)
	response, _ := groupDirectoryRequest(t, handler, testAuthenticator().AdminToken, f.group.ID, namespace.Id, "", 50)
	if response.Code != 400 {
		t.Fatalf("stale parent accepted: %d %s", response.Code, response.Body)
	}
	response, root := groupDirectoryRequest(t, handler, testAuthenticator().AdminToken, f.group.ID, "", "", 50)
	if response.Code != 200 || len(root.Items) != 0 {
		t.Fatalf("stale resolution evidence remained: %d %s", response.Code, response.Body)
	}
}

func TestGroupDirectoryPostgresHostedProxyUnionAndExactSnapshots(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	control, err := NewPostgresCacheControlStore(noBrowseObjectReads{NewMemoryOCIObjectStore(), t}, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close(); _ = control.Close() }()
	ctx := context.Background()
	seed := strings.ReplaceAll(uuid.NewString(), "-", "")
	hostedDigest := "sha256:" + seed + seed
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatMaven} {
		t.Run(string(format), func(t *testing.T) {
			hosted, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "hosted-" + uuid.NewString(), Format: format})
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "proxy-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeProxy, Endpoint: "https://cache.example.test/repository"})
			if err != nil {
				t.Fatal(err)
			}
			group := newBrowseGroup(t, store, format, proxy, hosted)
			t.Cleanup(func() {
				_, _ = control.db.Exec(`DELETE FROM hosted_groups WHERE id=$1`, group.ID)
				_, _ = control.db.Exec(`DELETE FROM cache_control_entries WHERE key LIKE $1`, string(format)+"/index/"+proxy.Name+"/%")
				for _, table := range []string{"native_maven_assets", "native_maven_artifacts", "native_raw_assets"} {
					_, _ = control.db.Exec(`DELETE FROM `+table+` WHERE repository_id=$1`, hosted.ID)
				}
				_, _ = control.db.Exec(`DELETE FROM native_raw_objects WHERE digest=$1`, hostedDigest)
				_, _ = control.db.Exec(`DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, hosted.ID, proxy.ID)
			})
			path := "docs/release%20notes.txt"
			if format == repository.FormatMaven {
				path = "com/acme/widget/1.0/widget-1.0.jar"
			}
			if format == repository.FormatRaw {
				_, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: hosted.ID, Path: path, Digest: hostedDigest, ObjectKey: uuid.NewString(), Size: 9})
			} else {
				_, err = control.db.Exec(`INSERT INTO native_maven_artifacts(id,repository_id,coordinate,digest,state) VALUES($1,$2,'com.acme:widget:1.0',$3,'visible')`, uuid.NewString(), hosted.ID, hostedDigest)
				if err == nil {
					_, err = control.db.Exec(`INSERT INTO native_maven_assets(repository_id,path,object_key,digest,size) VALUES($1,$2,$3,$4,9)`, hosted.ID, path, uuid.NewString(), hostedDigest)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			putBrowseIndex(t, control, proxy, path, nil)
			oci := NewDefaultOCICache(control, nil)
			handler := NewGatewayHandlerWithCacheMaintenance(Dependencies{}, store, TestAdapter{}, testAuthenticator(), oci, NewDefaultMavenCache(control, nil), NewCacheMaintenance(control, oci))
			parent := groupDirectoryOnlyItem(t, handler, group.ID, "")
			if format == repository.FormatMaven {
				parent = groupDirectoryOnlyItem(t, handler, group.ID, parent.Id)
				parent = groupDirectoryOnlyItem(t, handler, group.ID, parent.Id)
			}
			asset := groupDirectoryOnlyItem(t, handler, group.ID, parent.Id)
			if asset.Sources == nil || len(*asset.Sources) != 2 {
				t.Fatalf("union asset=%+v", asset)
			}
			sources := *asset.Sources
			if sources[0].RepositoryId.String() != hosted.ID || sources[0].ResolutionOrder != 1 || sources[1].RepositoryId.String() != proxy.ID || sources[1].ResolutionOrder != 2 {
				t.Fatalf("Hosted-first=%+v", sources)
			}
			if format != repository.FormatMaven {
				return
			}
			// Equal build numbers with different timestamps must remain separate nodes.
			for _, stamp := range []string{"20260907.010203", "20260907.020304"} {
				putBrowseIndex(t, control, proxy, "com/acme/widget/2.0-SNAPSHOT/widget-2.0-"+stamp+"-2.jar", nil)
			}
			_, err = control.db.Exec(`INSERT INTO native_maven_artifacts(id,repository_id,coordinate,digest,state,build_number,created_at) VALUES($1,$2,'com.acme:widget:2.0-SNAPSHOT',$3,'visible',2,'2026-09-07T01:02:03Z')`, uuid.NewString(), hosted.ID, hostedDigest)
			if err != nil {
				t.Fatal(err)
			}
			snapshotPath := "com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.010203-2.jar"
			_, err = control.db.Exec(`INSERT INTO native_maven_assets(repository_id,path,object_key,digest,size) VALUES($1,$2,$3,$4,9)`, hosted.ID, snapshotPath, uuid.NewString(), hostedDigest)
			if err != nil {
				t.Fatal(err)
			}
			namespace := groupDirectoryOnlyItem(t, handler, group.ID, "")
			component := groupDirectoryOnlyItem(t, handler, group.ID, namespace.Id)
			token := ""
			var versions []adminopenapi.BrowseNode
			for {
				response, page := groupDirectoryRequest(t, handler, testAuthenticator().AdminToken, group.ID, component.Id, token, 1)
				if response.Code != 200 {
					t.Fatalf("versions: %d %s", response.Code, response.Body)
				}
				versions = append(versions, page.Items...)
				if page.NextPageToken == nil {
					break
				}
				token = *page.NextPageToken
			}
			if len(versions) != 3 || versions[1].Name != "2.0-SNAPSHOT · 20260907.010203-2" || versions[2].Name != "2.0-SNAPSHOT · 20260907.020304-2" {
				t.Fatalf("versions=%+v", versions)
			}
			first := groupDirectoryOnlyItem(t, handler, group.ID, versions[1].Id)
			second := groupDirectoryOnlyItem(t, handler, group.ID, versions[2].Id)
			if len(*first.Sources) != 2 || len(*second.Sources) != 1 || *first.Path != snapshotPath || !strings.Contains(*second.Path, "020304") {
				t.Fatalf("mixed snapshots: %+v %+v", first, second)
			}
		})
	}
}
